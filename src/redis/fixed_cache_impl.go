package redis

import (
	"io"
	"math/rand"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ratelimit/src/stats"

	"github.com/coocood/freecache"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	logger "github.com/sirupsen/logrus"
	"golang.org/x/net/context"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/utils"
)

var tracer = otel.Tracer("redis.fixedCacheImpl")

type fixedRateLimitCacheImpl struct {
	client Client
	// Optional Client for a dedicated cache of per second limits.
	// If this client is nil, then the Cache will use the client for all
	// limits regardless of unit. If this client is not nil, then it
	// is used for limits that have a SECOND unit.
	perSecondClient                    Client
	stopCacheKeyIncrementWhenOverlimit bool
	baseRateLimiter                    *limiter.BaseRateLimiter

	// Hot key detection and batching
	hotKeyDetector   *HotKeyDetector
	hotKeyBatcher    *HotKeyBatcher
	perSecondBatcher *HotKeyBatcher
}

// HotKeyConfig holds configuration for hot key detection and batching.
type HotKeyConfig struct {
	Enabled           bool
	SketchMemoryBytes int
	SketchDepth       int
	Threshold         uint32
	MaxHotKeys        int
	FlushWindow       time.Duration
	DecayInterval     time.Duration
}

func pipelineAppend(client Client, pipeline *Pipeline, key string, hitsAddend uint64, result *uint64, expirationSeconds int64) {
	*pipeline = client.PipeAppend(*pipeline, result, "INCRBY", key, hitsAddend)
	*pipeline = client.PipeAppend(*pipeline, nil, "EXPIRE", key, expirationSeconds)
}

func pipelineAppendtoGet(client Client, pipeline *Pipeline, key string, result *uint64) {
	*pipeline = client.PipeAppend(*pipeline, result, "GET", key)
}

func (this *fixedRateLimitCacheImpl) getHitsAddend(hitsAddend uint64, isCacheKeyOverlimit, isCacheKeyNearlimit,
	isNearLimt bool,
) uint64 {
	// If stopCacheKeyIncrementWhenOverlimit is false, then we always increment the cache key.
	if !this.stopCacheKeyIncrementWhenOverlimit {
		return hitsAddend
	}

	// If stopCacheKeyIncrementWhenOverlimit is true, and one of the keys is over limit, then
	// we do not increment the cache key.
	if isCacheKeyOverlimit {
		return 0
	}

	// If stopCacheKeyIncrementWhenOverlimit is true, and none of the keys are over limit, then
	// to check if any of the keys are near limit. If none of the keys are near limit,
	// then we increment the cache key.
	if !isCacheKeyNearlimit {
		return hitsAddend
	}

	// If stopCacheKeyIncrementWhenOverlimit is true, and some of the keys are near limit, then
	// we only increment the cache key if the key is near limit.
	if isNearLimt {
		return hitsAddend
	}

	return 0
}

func (this *fixedRateLimitCacheImpl) DoLimit(
	ctx context.Context,
	request *pb.RateLimitRequest,
	limits []*config.RateLimit,
) []*pb.RateLimitResponse_DescriptorStatus {
	logger.Debugf("starting cache lookup")

	hitsAddends := utils.GetHitsAddends(request)

	// First build a list of all cache keys that we are actually going to hit.
	cacheKeys := this.baseRateLimiter.GenerateCacheKeys(request, limits, hitsAddends)

	isOverLimitWithLocalCache := make([]bool, len(request.Descriptors))
	results := make([]uint64, len(request.Descriptors))
	currentCount := make([]uint64, len(request.Descriptors))

	// For cluster support, we group pipelines by slot
	// Map: slot -> pipeline
	pipelines := make(map[uint16]Pipeline)
	perSecondPipelines := make(map[uint16]Pipeline)
	pipelinesToGet := make(map[uint16]Pipeline)
	perSecondPipelinesToGet := make(map[uint16]Pipeline)

	overlimitIndexes := make([]bool, len(request.Descriptors))
	nearlimitIndexes := make([]bool, len(request.Descriptors))
	isCacheKeyOverlimit := false
	isCacheKeyNearlimit := false

	// Check if any of the keys are already to the over limit in cache.
	for i, cacheKey := range cacheKeys {
		if cacheKey.Key == "" {
			continue
		}

		// Check if key is over the limit in local cache.
		if this.baseRateLimiter.IsOverLimitWithLocalCache(cacheKey.Key) {
			if limits[i].ShadowMode {
				logger.Debugf("Cache key %s would be rate limited but shadow mode is enabled on this rule", cacheKey.Key)
			} else {
				logger.Debugf("cache key is over the limit: %s", cacheKey.Key)
			}
			isCacheKeyOverlimit = true
			isOverLimitWithLocalCache[i] = true
			overlimitIndexes[i] = true
		}
	}

	// If none of the keys are over limit in local cache and the stopCacheKeyIncrementWhenOverlimit is true,
	// then we check if any of the keys are near limit in redis cache.
	if this.stopCacheKeyIncrementWhenOverlimit && !isCacheKeyOverlimit {
		for i, cacheKey := range cacheKeys {
			if cacheKey.Key == "" {
				continue
			}

			if this.perSecondClient != nil && cacheKey.PerSecond {
				slot := this.perSecondClient.GetSlot(cacheKey.Key)
				pipeline := perSecondPipelinesToGet[slot]
				pipelineAppendtoGet(this.perSecondClient, &pipeline, cacheKey.Key, &currentCount[i])
				perSecondPipelinesToGet[slot] = pipeline
			} else {
				slot := this.client.GetSlot(cacheKey.Key)
				pipeline := pipelinesToGet[slot]
				pipelineAppendtoGet(this.client, &pipeline, cacheKey.Key, &currentCount[i])
				pipelinesToGet[slot] = pipeline
			}
		}

		// Execute all GET pipelines grouped by slot
		for _, pipeline := range pipelinesToGet {
			if len(pipeline) > 0 {
				checkError(this.client.PipeDo(pipeline))
			}
		}
		for _, pipeline := range perSecondPipelinesToGet {
			if len(pipeline) > 0 {
				checkError(this.perSecondClient.PipeDo(pipeline))
			}
		}

		for i, cacheKey := range cacheKeys {
			if cacheKey.Key == "" {
				continue
			}
			// Now fetch the pipeline.
			limitBeforeIncrease := currentCount[i]
			limitAfterIncrease := limitBeforeIncrease + hitsAddends[i]

			limitInfo := limiter.NewRateLimitInfo(limits[i], limitBeforeIncrease, limitAfterIncrease, 0, 0)

			if this.baseRateLimiter.IsOverLimitThresholdReached(limitInfo) {
				nearlimitIndexes[i] = true
				isCacheKeyNearlimit = true
			}
		}
	}

	// Track hot key result channels for async results
	hotKeyResultChans := make(map[int]<-chan HotKeyBatcherResult)

	// Now, actually setup the pipeline to increase the usage of cache key, skipping empty cache keys.
	for i, cacheKey := range cacheKeys {
		if cacheKey.Key == "" || overlimitIndexes[i] {
			continue
		}

		logger.Debugf("looking up cache key: %s", cacheKey.Key)

		expirationSeconds := utils.UnitToDivider(limits[i].Limit.Unit)
		if this.baseRateLimiter.ExpirationJitterMaxSeconds > 0 {
			expirationSeconds += this.baseRateLimiter.JitterRand.Int63n(this.baseRateLimiter.ExpirationJitterMaxSeconds)
		}

		hitsAddend := this.getHitsAddend(hitsAddends[i], isCacheKeyOverlimit, isCacheKeyNearlimit, nearlimitIndexes[i])

		// Use the perSecondConn if it is not nil and the cacheKey represents a per second Limit.
		if this.perSecondClient != nil && cacheKey.PerSecond {
			// Check if this is a hot key and should be batched
			if this.hotKeyDetector != nil && this.perSecondBatcher != nil && this.hotKeyDetector.RecordAccess(cacheKey.Key) {
				// Hot key: submit to batcher for 300us flush window
				logger.Debugf("hot key detected (per-second): %s", cacheKey.Key)
				hotKeyResultChans[i] = this.perSecondBatcher.Submit(cacheKey.Key, hitsAddend, expirationSeconds)
			} else {
				// Normal key: add to pipeline (grouped by slot for cluster support)
				slot := this.perSecondClient.GetSlot(cacheKey.Key)
				pipeline := perSecondPipelines[slot]
				pipelineAppend(this.perSecondClient, &pipeline, cacheKey.Key, hitsAddend, &results[i], expirationSeconds)
				perSecondPipelines[slot] = pipeline
			}
		} else {
			// Check if this is a hot key and should be batched
			if this.hotKeyDetector != nil && this.hotKeyBatcher != nil && this.hotKeyDetector.RecordAccess(cacheKey.Key) {
				// Hot key: submit to batcher for 300us flush window
				logger.Debugf("hot key detected: %s", cacheKey.Key)
				hotKeyResultChans[i] = this.hotKeyBatcher.Submit(cacheKey.Key, hitsAddend, expirationSeconds)
			} else {
				// Normal key: add to pipeline (grouped by slot for cluster support)
				slot := this.client.GetSlot(cacheKey.Key)
				pipeline := pipelines[slot]
				pipelineAppend(this.client, &pipeline, cacheKey.Key, hitsAddend, &results[i], expirationSeconds)
				pipelines[slot] = pipeline
			}
		}
	}

	// Calculate total pipeline lengths for tracing
	totalPipelineLen := 0
	for _, p := range pipelines {
		totalPipelineLen += len(p)
	}
	totalPerSecondPipelineLen := 0
	for _, p := range perSecondPipelines {
		totalPerSecondPipelineLen += len(p)
	}

	// Generate trace
	_, span := tracer.Start(ctx, "Redis Pipeline Execution",
		trace.WithAttributes(
			attribute.Int("pipeline length", totalPipelineLen),
			attribute.Int("perSecondPipeline length", totalPerSecondPipelineLen),
			attribute.Int("hotKeyBatched count", len(hotKeyResultChans)),
			attribute.Int("pipeline slots", len(pipelines)),
			attribute.Int("perSecondPipeline slots", len(perSecondPipelines)),
		),
	)
	defer span.End()

	// Execute all pipelines grouped by slot in parallel
	var wg sync.WaitGroup
	var pipelineErr error
	var errMutex sync.Mutex

	// Execute regular pipelines in parallel
	for _, pipeline := range pipelines {
		if len(pipeline) > 0 {
			wg.Add(1)
			go func(p Pipeline) {
				defer wg.Done()
				if err := this.client.PipeDo(p); err != nil {
					errMutex.Lock()
					if pipelineErr == nil {
						pipelineErr = err
					}
					errMutex.Unlock()
				}
			}(pipeline)
		}
	}

	// Execute per-second pipelines in parallel
	for _, pipeline := range perSecondPipelines {
		if len(pipeline) > 0 {
			wg.Add(1)
			go func(p Pipeline) {
				defer wg.Done()
				if err := this.perSecondClient.PipeDo(p); err != nil {
					errMutex.Lock()
					if pipelineErr == nil {
						pipelineErr = err
					}
					errMutex.Unlock()
				}
			}(pipeline)
		}
	}

	// Wait for all pipelines to complete
	wg.Wait()
	if pipelineErr != nil {
		checkError(pipelineErr)
	}

	// Wait for hot key batched results
	for i, resultChan := range hotKeyResultChans {
		batchResult := <-resultChan
		if batchResult.Err != nil {
			checkError(batchResult.Err)
		}
		results[i] = batchResult.Value
	}

	// Now fetch the pipeline.
	responseDescriptorStatuses := make([]*pb.RateLimitResponse_DescriptorStatus,
		len(request.Descriptors))
	for i, cacheKey := range cacheKeys {

		limitAfterIncrease := results[i]
		limitBeforeIncrease := limitAfterIncrease - hitsAddends[i]

		limitInfo := limiter.NewRateLimitInfo(limits[i], limitBeforeIncrease, limitAfterIncrease, 0, 0)

		responseDescriptorStatuses[i] = this.baseRateLimiter.GetResponseDescriptorStatus(cacheKey.Key,
			limitInfo, isOverLimitWithLocalCache[i], hitsAddends[i])

	}

	return responseDescriptorStatuses
}

// Flush flushes any pending hot key batches.
func (this *fixedRateLimitCacheImpl) Flush() {
	// Hot key batchers are flushed automatically on their timer,
	// but we can trigger a manual flush if needed.
}

// Close stops the hot key batchers.
func (this *fixedRateLimitCacheImpl) Close() error {
	if this.hotKeyBatcher != nil {
		this.hotKeyBatcher.Stop()
	}
	if this.perSecondBatcher != nil {
		this.perSecondBatcher.Stop()
	}
	return nil
}

// Ensure fixedRateLimitCacheImpl implements io.Closer
var _ io.Closer = (*fixedRateLimitCacheImpl)(nil)

func NewFixedRateLimitCacheImpl(client Client, perSecondClient Client, timeSource utils.TimeSource,
	jitterRand *rand.Rand, expirationJitterMaxSeconds int64, localCache *freecache.Cache, nearLimitRatio float32, cacheKeyPrefix string, statsManager stats.Manager,
	stopCacheKeyIncrementWhenOverlimit bool, hotKeyConfig *HotKeyConfig,
) limiter.RateLimitCache {
	impl := &fixedRateLimitCacheImpl{
		client:                             client,
		perSecondClient:                    perSecondClient,
		stopCacheKeyIncrementWhenOverlimit: stopCacheKeyIncrementWhenOverlimit,
		baseRateLimiter:                    limiter.NewBaseRateLimit(timeSource, jitterRand, expirationJitterMaxSeconds, localCache, nearLimitRatio, cacheKeyPrefix, statsManager),
	}

	// Initialize hot key detection if enabled
	if hotKeyConfig != nil && hotKeyConfig.Enabled {
		detectorConfig := HotKeyDetectorConfig{
			SketchMemoryBytes: hotKeyConfig.SketchMemoryBytes,
			SketchDepth:       hotKeyConfig.SketchDepth,
			HotThreshold:      hotKeyConfig.Threshold,
			MaxHotKeys:        hotKeyConfig.MaxHotKeys,
			DecayInterval:     hotKeyConfig.DecayInterval,
			DecayFactor:       0.5,
		}
		impl.hotKeyDetector = NewHotKeyDetector(detectorConfig)

		impl.hotKeyBatcher = NewHotKeyBatcher(client, hotKeyConfig.FlushWindow)
		impl.hotKeyBatcher.Start()

		if perSecondClient != nil {
			impl.perSecondBatcher = NewHotKeyBatcher(perSecondClient, hotKeyConfig.FlushWindow)
			impl.perSecondBatcher.Start()
		}

		logger.Warnf("Hot key detection enabled with threshold=%d, flush_window=%v, sketch_memory=%d bytes",
			hotKeyConfig.Threshold, hotKeyConfig.FlushWindow, hotKeyConfig.SketchMemoryBytes)
	}

	return impl
}

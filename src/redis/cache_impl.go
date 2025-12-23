package redis

import (
	"io"
	"math/rand"

	"github.com/coocood/freecache"

	"github.com/envoyproxy/ratelimit/src/limiter"
	"github.com/envoyproxy/ratelimit/src/server"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"
	"github.com/envoyproxy/ratelimit/src/utils"
)

func NewRateLimiterCacheImplFromSettings(s settings.Settings, localCache *freecache.Cache, srv server.Server, timeSource utils.TimeSource, jitterRand *rand.Rand, expirationJitterMaxSeconds int64, statsManager stats.Manager) (limiter.RateLimitCache, io.Closer) {
	closer := &utils.MultiCloser{}
	var perSecondPool Client
	if s.RedisPerSecond {
		perSecondPool = NewClientImpl(srv.Scope().Scope("redis_per_second_pool"), s.RedisPerSecondTls, s.RedisPerSecondAuth, s.RedisPerSecondSocketType,
			s.RedisPerSecondType, s.RedisPerSecondUrl, s.RedisPerSecondPoolSize, s.RedisTlsConfig, s.RedisHealthCheckActiveConnection, srv, s.RedisPerSecondTimeout,
			s.RedisPerSecondPoolOnEmptyBehavior, s.RedisPerSecondPoolOnEmptyWaitDuration, s.RedisPerSecondSentinelAuth)
		closer.Closers = append(closer.Closers, perSecondPool)
	}

	otherPool := NewClientImpl(srv.Scope().Scope("redis_pool"), s.RedisTls, s.RedisAuth, s.RedisSocketType, s.RedisType, s.RedisUrl, s.RedisPoolSize,
		s.RedisTlsConfig, s.RedisHealthCheckActiveConnection, srv, s.RedisTimeout,
		s.RedisPoolOnEmptyBehavior, s.RedisPoolOnEmptyWaitDuration, s.RedisSentinelAuth)
	closer.Closers = append(closer.Closers, otherPool)

	// Configure hot key detection if enabled
	var hotKeyConfig *HotKeyConfig
	if s.HotKeyDetectionEnabled {
		hotKeyConfig = &HotKeyConfig{
			Enabled:           true,
			SketchMemoryBytes: s.HotKeySketchMemoryBytes,
			SketchDepth:       s.HotKeySketchDepth,
			Threshold:         s.HotKeyThreshold,
			MaxHotKeys:        s.HotKeyMaxCount,
			FlushWindow:       s.HotKeyFlushWindow,
			DecayInterval:     s.HotKeyDecayInterval,
		}
	}

	cache := NewFixedRateLimitCacheImpl(
		otherPool,
		perSecondPool,
		timeSource,
		jitterRand,
		expirationJitterMaxSeconds,
		localCache,
		s.NearLimitRatio,
		s.CacheKeyPrefix,
		statsManager,
		s.StopCacheKeyIncrementWhenOverlimit,
		hotKeyConfig,
	)

	// Add cache closer for hot key batchers
	if cacheCloser, ok := cache.(io.Closer); ok {
		closer.Closers = append(closer.Closers, cacheCloser)
	}

	return cache, closer
}

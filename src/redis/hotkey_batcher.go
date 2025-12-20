package redis

import (
	"sync"
	"time"
)

// HotKeyBatcherResult holds the result of a batched operation.
type HotKeyBatcherResult struct {
	Value uint64
	Err   error
}

// pendingWaiter represents a single request waiting for a batched result.
type pendingWaiter struct {
	hitsAddend uint64
	resultChan chan HotKeyBatcherResult
}

// aggregatedIncrement holds the aggregated increment for a key.
type aggregatedIncrement struct {
	totalHits         uint64
	expirationSeconds int64
	waiters           []*pendingWaiter
}

// HotKeyBatcher batches INCRBY and EXPIRE commands for hot keys
// and flushes them periodically (e.g., every 300 microseconds).
type HotKeyBatcher struct {
	client      Client
	flushWindow time.Duration
	pending     map[string]*aggregatedIncrement
	mu          sync.Mutex
	ticker      *time.Ticker
	stopChan    chan struct{}
	wg          sync.WaitGroup
	running     bool
}

// NewHotKeyBatcher creates a new hot key batcher.
func NewHotKeyBatcher(client Client, flushWindow time.Duration) *HotKeyBatcher {
	if flushWindow <= 0 {
		flushWindow = 300 * time.Microsecond
	}

	return &HotKeyBatcher{
		client:      client,
		flushWindow: flushWindow,
		pending:     make(map[string]*aggregatedIncrement),
		stopChan:    make(chan struct{}),
	}
}

// Start begins the background flush goroutine.
func (b *HotKeyBatcher) Start() {
	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return
	}
	b.running = true
	b.ticker = time.NewTicker(b.flushWindow)
	b.mu.Unlock()

	b.wg.Add(1)
	go b.flushLoop()
}

// Stop stops the batcher and flushes any remaining pending operations.
func (b *HotKeyBatcher) Stop() {
	b.mu.Lock()
	if !b.running {
		b.mu.Unlock()
		return
	}
	b.running = false
	b.mu.Unlock()

	close(b.stopChan)
	b.wg.Wait()

	if b.ticker != nil {
		b.ticker.Stop()
	}
}

// flushLoop runs the periodic flush.
func (b *HotKeyBatcher) flushLoop() {
	defer b.wg.Done()

	for {
		select {
		case <-b.ticker.C:
			b.flush()
		case <-b.stopChan:
			// Final flush before stopping
			b.flush()
			return
		}
	}
}

// Submit adds a key increment to the batch and returns a channel that will receive the result.
// The caller should wait on the returned channel to get the final count.
func (b *HotKeyBatcher) Submit(key string, hitsAddend uint64, expirationSeconds int64) <-chan HotKeyBatcherResult {
	resultChan := make(chan HotKeyBatcherResult, 1)

	waiter := &pendingWaiter{
		hitsAddend: hitsAddend,
		resultChan: resultChan,
	}

	b.mu.Lock()
	agg, exists := b.pending[key]
	if !exists {
		agg = &aggregatedIncrement{
			expirationSeconds: expirationSeconds,
			waiters:           make([]*pendingWaiter, 0, 4),
		}
		b.pending[key] = agg
	}

	agg.totalHits += hitsAddend
	// Use the maximum expiration time
	if expirationSeconds > agg.expirationSeconds {
		agg.expirationSeconds = expirationSeconds
	}
	agg.waiters = append(agg.waiters, waiter)
	b.mu.Unlock()

	return resultChan
}

// flush sends all pending operations to Redis in a single pipeline.
func (b *HotKeyBatcher) flush() {
	b.mu.Lock()
	if len(b.pending) == 0 {
		b.mu.Unlock()
		return
	}

	// Swap pending map with a new one
	toFlush := b.pending
	b.pending = make(map[string]*aggregatedIncrement)
	b.mu.Unlock()

	// Build pipeline for all pending keys
	var pipeline Pipeline
	results := make(map[string]*uint64)

	for key, agg := range toFlush {
		var result uint64
		results[key] = &result
		pipeline = b.client.PipeAppend(pipeline, &result, "INCRBY", key, agg.totalHits)
		pipeline = b.client.PipeAppend(pipeline, nil, "EXPIRE", key, agg.expirationSeconds)
	}

	// Execute pipeline
	err := b.client.PipeDo(pipeline)

	// Distribute results to all waiters with per-request counts
	// Each waiter gets their own "limitAfterIncrease" value
	for key, agg := range toFlush {
		if err != nil {
			// On error, send error to all waiters
			for _, waiter := range agg.waiters {
				waiter.resultChan <- HotKeyBatcherResult{Err: err}
				close(waiter.resultChan)
			}
			continue
		}

		// Calculate per-waiter results
		// finalCount is the count after all increments in this batch
		// We need to give each waiter their individual "after" count
		//
		// Example: if initial count was 50, and we have 3 waiters with hitsAddend 2, 3, 1:
		// - Total increment = 6, finalCount = 56
		// - waiter[0] (first): gets 52 (50 + 2)
		// - waiter[1] (second): gets 55 (52 + 3)
		// - waiter[2] (third): gets 56 (55 + 1)
		//
		// We calculate backwards from finalCount:
		// - Start with finalCount (56)
		// - waiter[2]: 56, then subtract 1 for next
		// - waiter[1]: 55, then subtract 3 for next
		// - waiter[0]: 52
		finalCount := *results[key]
		runningCount := finalCount

		// First, calculate each waiter's result going backwards
		waiterResults := make([]uint64, len(agg.waiters))
		for i := len(agg.waiters) - 1; i >= 0; i-- {
			waiterResults[i] = runningCount
			runningCount -= agg.waiters[i].hitsAddend
		}

		// Send results to each waiter
		for i, waiter := range agg.waiters {
			waiter.resultChan <- HotKeyBatcherResult{Value: waiterResults[i]}
			close(waiter.resultChan)
		}
	}
}

// PendingCount returns the number of keys currently pending in the batch.
func (b *HotKeyBatcher) PendingCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}

// PendingWaiterCount returns the total number of waiters across all pending keys.
func (b *HotKeyBatcher) PendingWaiterCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	count := 0
	for _, agg := range b.pending {
		count += len(agg.waiters)
	}
	return count
}

// FlushWindow returns the configured flush window duration.
func (b *HotKeyBatcher) FlushWindow() time.Duration {
	return b.flushWindow
}

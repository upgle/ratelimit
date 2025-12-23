# Parallel Pipeline Execution for Redis Cluster

## Overview

This proposal optimizes multi-slot operations by executing pipelines for different slots concurrently, reducing latency caused by sequential network round-trips.

## Problem Statement

After implementing slot-based grouping (see [01-slot-based-grouping.md](./01-slot-based-grouping.md)), multi-slot operations execute pipelines sequentially:

```go
// Sequential execution (BEFORE)
for _, pipeline := range pipelines {
    checkError(this.client.PipeDo(pipeline))  // Blocks until complete
}
```

### Latency Accumulation

For requests with keys distributed across N slots:
```
Total Latency = Latency(slot1) + Latency(slot2) + ... + Latency(slotN)
```

**Example: 5 slots, 2ms latency each**
```
Sequential: 2ms + 2ms + 2ms + 2ms + 2ms = 10ms
Parallel:   max(2ms, 2ms, 2ms, 2ms, 2ms) = 2ms
```

## Solution

### Concurrent Execution with Goroutines

Execute all slot pipelines concurrently using goroutines and WaitGroup:

```go
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

// Wait for all pipelines to complete
wg.Wait()
if pipelineErr != nil {
    checkError(pipelineErr)
}
```

## Implementation Details

### Files Modified

1. **src/redis/fixed_cache_impl.go**
   - Added `sync` import
   - Replaced sequential loops with goroutines
   - Added WaitGroup for synchronization
   - Implemented mutex-protected error handling

### Error Handling

- **First Error Wins**: Only the first error is captured and returned
- **Thread-Safe**: Mutex protects concurrent error writes
- **Clean Panic**: Maintains existing `checkError()` behavior

### Goroutine Management

- **Scope Capture**: Pipeline copied to avoid closure issues
- **Proper Cleanup**: `defer wg.Done()` ensures counter decrements
- **Resource Bounded**: Limited by number of slots (max ~10-20 goroutines)

## Performance Impact

### Before (Sequential Execution)

```
mixed_10keys (5-7 slots distributed):
- RPS: 3,000
- Avg Latency: 33.2ms
- P95 Latency: 41.2ms
- P99 Latency: 47.5ms
```

### After (Parallel Execution)

```
mixed_10keys (5-7 slots distributed):
- RPS: 3,813 (+27%)
- Avg Latency: 26.4ms (-20%)
- P95 Latency: 36.2ms (-12%)
- P99 Latency: 47.6ms (~0%)
```

### Improvement Analysis

| Metric | Sequential | Parallel | Improvement |
|--------|-----------|----------|-------------|
| **RPS** | 3,000 | 3,813 | **+27%** |
| **Avg Latency** | 33.2ms | 26.4ms | **-20%** |
| **P95 Latency** | 41.2ms | 36.2ms | **-12%** |
| **P99 Latency** | 47.5ms | 47.6ms | ~0% |

## Timeline Visualization

```
Sequential Execution (5 slots):
├─ slot1 ─┤ 2ms
           ├─ slot2 ─┤ 2ms
                      ├─ slot3 ─┤ 2ms
                                 ├─ slot4 ─┤ 2ms
                                            ├─ slot5 ─┤ 2ms
Total: 10ms (minimum, no network overhead)
Actual: 33ms (with network overhead)

Parallel Execution (5 slots):
├─ slot1 ─┤
├─ slot2 ─┤
├─ slot3 ─┤ All concurrent, 2ms max
├─ slot4 ─┤
├─ slot5 ─┤
Total: 2ms (minimum, no network overhead)
Actual: 26ms (with network overhead)

Improvement: 33ms → 26ms (-21%)
```

## Benefits

1. **Reduced Latency**: 20% reduction in average latency for multi-slot operations
2. **Higher Throughput**: 27% increase in RPS
3. **Better Resource Utilization**: Network and CPU work in parallel
4. **Scalability**: Improvement increases with number of slots

## Trade-offs

1. **Goroutine Overhead**: Small CPU and memory cost per goroutine
2. **Connection Pool Pressure**: Multiple concurrent requests to Redis
3. **Complexity**: More complex error handling and synchronization

## Why Still Slower Than Few-Slot Operations?

### mixed_2keys vs mixed_10keys

```
mixed_2keys: 13.8k RPS, 7.2ms latency
mixed_10keys: 3.8k RPS, 26.4ms latency

Ratio: 26.4 / 7.2 = 3.67x slower
```

**Reasons:**
1. **Work Volume**: 10 keys = 20 Redis commands vs 4 commands (5x more work)
2. **Slot Distribution**: 5-7 slots vs 1-2 slots (3-4x more network calls)
3. **Redis Load**: More nodes involved = higher cluster resource consumption

**Efficiency Gain:**
```
Without parallelization: 5x work → 5x latency
With parallelization: 5x work → 3.67x latency
Efficiency improvement: ~27%
```

## Testing

### Performance Test

```bash
# Build
make compile

# Run test
./scripts/run-perf-test.sh -e test/perf/endpoints-custom-test.yaml

# Compare results
# Sequential: ~3.0k RPS, ~33ms
# Parallel:   ~3.8k RPS, ~26ms
```

### Stress Test

```bash
# High concurrency test
CONCURRENCY=200 DURATION=30s ./scripts/run-perf-test.sh \
  -e test/perf/endpoints-custom-test.yaml
```

## Monitoring

### Key Metrics

- **Goroutine Count**: Monitor with `runtime.NumGoroutine()`
- **Connection Pool**: Check `NumActiveConns()`
- **Redis Load**: Monitor commands/sec per node
- **Latency Distribution**: Track P50, P95, P99

### Expected Behavior

- **Goroutines**: Spike during requests, return to baseline
- **Connections**: Should stay within pool limits
- **Redis**: Distributed load across cluster nodes

## Production Considerations

1. **Connection Pool Size**: Ensure pool can handle concurrent requests
   ```go
   REDIS_POOL_SIZE: 10  // Adjust based on slot count
   ```

2. **Timeout Configuration**: Set appropriate timeouts
   ```go
   REDIS_TIMEOUT: 1s  // Balance between responsiveness and retry
   ```

3. **Monitoring**: Alert on high P99 latency or error rates

4. **Gradual Rollout**: Test with small traffic percentage first

## Commit

```
perf: implement parallel pipeline execution for Redis Cluster

Commit: b5d43c5
```

## See Also

- [01-slot-based-grouping.md](./01-slot-based-grouping.md) - Foundation for this optimization
- [05-performance-analysis.md](./05-performance-analysis.md) - Detailed performance analysis

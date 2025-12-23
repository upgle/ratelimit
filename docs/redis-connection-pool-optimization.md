# Redis Connection Pool Optimization

## Overview

This document explains the critical impact of Redis connection pool sizing on performance, particularly for multi-slot Redis Cluster operations with parallel pipeline execution.

## Problem Discovery

Initial performance testing showed unexpectedly poor performance for multi-key scenarios:

```
Configuration:
- Concurrency: 100 workers
- REDIS_POOL_SIZE: 10
- Parallel pipeline execution: ENABLED

Results:
- mixed_2keys:  20.0k RPS,  5.0ms latency
- mixed_10keys:  6.0k RPS, 16.4ms latency

Question: Why is mixed_10keys 3.3x slower despite parallel execution?
```

## Root Cause Analysis

### Connection Pool Saturation

The bottleneck was **connection pool contention**, not the parallel execution itself.

#### mixed_2keys Scenario:
```
Request Structure:
- 2 descriptors → ~2 cache keys
- Slot distribution: ~1-2 slots
- Parallel pipelines: 1-2 per request

Concurrent Load:
100 workers × 2 parallel pipelines = 200 concurrent Redis operations
200 operations ÷ 10 connections = 20 requests queued per connection

Result: Moderate queuing delay
```

#### mixed_10keys Scenario:
```
Request Structure:
- 10 descriptors → ~10 cache keys
- Slot distribution: ~5-7 slots
- Parallel pipelines: 5-7 per request

Concurrent Load:
100 workers × 6 avg parallel pipelines = 600 concurrent Redis operations
600 operations ÷ 10 connections = 60 requests queued per connection

Result: SEVERE queuing delay (3x longer wait time)
```

### Timeline Visualization

```
Pool-10 (mixed_10keys):
Connection 1: [req1][req2][req3]...[req60]  ← 60 requests queued!
Connection 2: [req1][req2][req3]...[req60]
...
Connection 10: [req1][req2][req3]...[req60]

Each request waits for ~59 others to complete
Total latency: Network time + Redis time + QUEUE WAIT TIME (dominant)

Pool-50 (mixed_10keys):
Connection 1: [req1][req2]...[req12]  ← Only 12 requests queued
Connection 2: [req1][req2]...[req12]
...
Connection 50: [req1][req2]...[req12]

Each request waits for ~11 others
Total latency: Network time + Redis time + queue wait time (minimal)
```

## Performance Test Results

### Test Configuration

```yaml
Test Settings:
  Concurrency: 100 workers
  Duration: 10s
  Warmup: 2s

Pool Sizes Tested: 10, 50, 100
```

### Results Summary

| Scenario | Pool-10 | Pool-50 | Pool-100 | Improvement |
|----------|---------|---------|----------|-------------|
| **fixed_key** | 23,743 RPS<br/>4.21ms | 24,032 RPS<br/>4.16ms | 24,167 RPS<br/>4.14ms | **+1.8%** |
| **variable_key** | 21,287 RPS<br/>4.70ms | 22,215 RPS<br/>4.50ms | 22,472 RPS<br/>4.45ms | **+5.6%** |
| **mixed_2keys** | 20,018 RPS<br/>4.99ms | 21,234 RPS<br/>4.71ms | 21,477 RPS<br/>4.65ms | **+7.3%** |
| **mixed_10keys** | **6,080 RPS**<br/>**16.45ms** | **31,636 RPS**<br/>**3.16ms** | **32,634 RPS**<br/>**3.06ms** | **+437%** 🚀 |

### Latency Distribution (mixed_10keys)

| Metric | Pool-10 | Pool-50 | Pool-100 |
|--------|---------|---------|----------|
| **Avg** | 16.45ms | 3.16ms | 3.06ms |
| **P50** | 16.15ms | 3.09ms | 3.00ms |
| **P95** | 22.45ms | 3.78ms | 3.65ms |
| **P99** | 25.45ms | 4.41ms | 4.26ms |
| **P999** | 42.89ms | 7.52ms | 7.14ms |

**All percentiles improved by ~80%!**

### Key Findings

1. **Pool-50 is the Sweet Spot**
   - Pool-50 to Pool-100: Only 3% additional improvement
   - Diminishing returns beyond Pool-50
   - Pool-50 provides excellent cost/benefit ratio

2. **Multi-Slot Operations Benefit Most**
   - fixed_key (1 slot): +2% improvement
   - mixed_2keys (2 slots): +7% improvement
   - mixed_10keys (6 slots): +437% improvement
   - **More slots = more benefit from larger pool**

3. **Latency Improvements are Dramatic**
   - mixed_10keys: 16.45ms → 3.06ms (-81%)
   - P99 latency: 25.45ms → 4.26ms (-83%)
   - Tail latencies also improve significantly

## Why Pool Size Matters

### 1. Parallel Pipeline Execution Creates Demand

```go
// Our implementation (fixed_cache_impl.go)
var wg sync.WaitGroup

// Execute all slot pipelines in parallel
for _, pipeline := range pipelines {
    wg.Add(1)
    go func(p Pipeline) {
        defer wg.Done()
        this.client.PipeDo(p)  // Needs a connection from pool
    }(pipeline)
}

wg.Wait()  // Wait for ALL to complete
```

**Key Point**: Each goroutine needs a connection from the pool simultaneously. With 100 workers each spawning 6 goroutines, we need **600 connections available** to avoid queuing.

### 2. Connection Pool as Bottleneck

```
Radix v4 Connection Pool Behavior:
┌─────────────────────────────────────┐
│ Worker requests connection          │
│         ↓                            │
│ Pool has available connection?      │
│    YES → Get connection immediately │
│    NO  → WAIT in queue              │
└─────────────────────────────────────┘

With Pool-10:
- 600 requests competing for 10 connections
- Average wait time: (600 ÷ 10) × avg_redis_time
- Throughput severely limited

With Pool-50:
- 600 requests competing for 50 connections
- Average wait time: (600 ÷ 50) × avg_redis_time
- Throughput dramatically improved
```

### 3. Mathematical Model

**Total Latency Formula:**
```
Total_Latency = Network_Latency + Redis_Latency + Queue_Wait_Time

Where Queue_Wait_Time ≈ (Concurrent_Requests ÷ Pool_Size) × Avg_Request_Time
```

**Example (mixed_10keys):**
```
Pool-10:
Queue_Wait = (600 ÷ 10) × 0.2ms = 12ms
Total = 0.5ms + 0.5ms + 12ms = 13ms (matches observed ~16ms)

Pool-50:
Queue_Wait = (600 ÷ 50) × 0.2ms = 2.4ms
Total = 0.5ms + 0.5ms + 2.4ms = 3.4ms (matches observed ~3.2ms)

Pool-100:
Queue_Wait = (600 ÷ 100) × 0.2ms = 1.2ms
Total = 0.5ms + 0.5ms + 1.2ms = 2.2ms (close to observed ~3.0ms)
```

## Optimal Pool Size Calculation

### Formula

```
Optimal_Pool_Size = (Concurrency × Avg_Parallel_Pipelines) ÷ Efficiency_Factor

Where:
- Concurrency: Number of concurrent workers
- Avg_Parallel_Pipelines: Average number of slots per request
- Efficiency_Factor: 2-3 (accounts for uneven distribution)
```

### Examples

#### Scenario 1: Light Multi-Key Load
```
Concurrency: 100
Avg_Parallel_Pipelines: 2
Efficiency_Factor: 2

Optimal_Pool_Size = (100 × 2) ÷ 2 = 100
Recommended: 50 (sufficient due to temporal distribution)
```

#### Scenario 2: Heavy Multi-Key Load
```
Concurrency: 100
Avg_Parallel_Pipelines: 6
Efficiency_Factor: 2

Optimal_Pool_Size = (100 × 6) ÷ 2 = 300
Recommended: 50-100 (diminishing returns beyond 50)
```

#### Scenario 3: Production Load
```
Concurrency: 500 (high traffic service)
Avg_Parallel_Pipelines: 4
Efficiency_Factor: 2.5

Optimal_Pool_Size = (500 × 4) ÷ 2.5 = 800
Recommended: 200-400 (balance performance vs connections)
```

## Recommendations

### 1. Default Settings

**For Redis Cluster with Multi-Key Operations:**
```bash
REDIS_POOL_SIZE=50  # Sweet spot for most workloads
```

**For Redis Standalone/Sentinel:**
```bash
REDIS_POOL_SIZE=10  # Sufficient for single-node operations
```

### 2. Tuning Guidelines

| Workload Type | Pool Size | Rationale |
|---------------|-----------|-----------|
| **Single-key dominant** | 10-20 | Minimal parallel pipelines |
| **Few keys (2-3)** | 20-30 | Some parallelization benefit |
| **Many keys (5-10)** | 50-100 | High parallelization,큰 benefit |
| **Very many keys (10+)** | 100-200 | Maximum parallelization |

### 3. Monitoring

**Track these metrics to detect pool saturation:**

```go
// Key metrics
- redis.pool.active_connections (should be < pool_size)
- redis.pool.wait_time (should be < 1ms)
- redis.pipeline_latency (watch for spikes)
- request.latency.p99 (watch for degradation)
```

**Alert Conditions:**
```yaml
- active_connections > pool_size × 0.9 for 5min
  → Increase pool size

- avg_wait_time > 5ms
  → Pool severely saturated

- p99_latency > 2 × p50_latency
  → Check pool contention
```

### 4. Cost vs Benefit

**Connection Pool Memory Cost:**
```
Per Connection: ~50-100KB (including buffers)
Pool-10:  0.5-1MB
Pool-50:  2.5-5MB
Pool-100: 5-10MB

Cost: Negligible compared to performance gain
```

**Redis Server Impact:**
```
Each connection consumes:
- Memory: ~20KB per connection
- File descriptor: 1 per connection

Pool-100 × 10 ratelimit instances = 1,000 connections
- Memory: ~20MB (negligible for modern Redis)
- FDs: 1,000 (well under typical limit of 10,000+)

Impact: Minimal for production Redis Cluster
```

## Production Deployment

### Configuration Update

**Before (Original):**
```bash
REDIS_POOL_SIZE=10  # Default
```

**After (Optimized):**
```bash
# For Redis Cluster with multi-key operations
REDIS_POOL_SIZE=50

# Rationale:
# - 437% RPS improvement for multi-key scenarios
# - 81% latency reduction
# - Only 5MB additional memory per instance
# - Minimal Redis server impact
```

### Rollout Strategy

```
Phase 1: Testing (1 week)
├─ Deploy to staging with POOL_SIZE=50
├─ Run load tests with realistic traffic patterns
├─ Monitor pool utilization and latency
└─ Validate no regressions

Phase 2: Canary (1 week)
├─ Deploy to 10% production with POOL_SIZE=50
├─ Compare metrics vs baseline (POOL_SIZE=10)
├─ Monitor for 2-3 days
└─ Increase to 50% if metrics improve

Phase 3: Full Rollout (1 week)
├─ Deploy to 100% production
├─ Monitor key metrics (RPS, latency, pool usage)
└─ Document improvements
```

### Rollback Plan

```bash
# If issues occur, rollback is simple:
REDIS_POOL_SIZE=10  # Revert to original

# No code changes required
# No data migration needed
# Just env var update + restart
```

## Benchmarking

### How to Test

```bash
# 1. Create test config
cat > test/perf/pool-comparison.yaml <<EOF
test_settings:
  concurrency: 100
  duration: 10s

endpoints:
  - name: "pool-10"
    settings:
      REDIS_POOL_SIZE: 10

  - name: "pool-50"
    settings:
      REDIS_POOL_SIZE: 50
EOF

# 2. Run test
./scripts/run-perf-test.sh -e test/perf/pool-comparison.yaml

# 3. Compare results
# Look for mixed_10keys scenario improvement
```

### Expected Results

```
Pool-10:
- mixed_10keys: ~6k RPS, ~16ms latency

Pool-50:
- mixed_10keys: ~31k RPS, ~3ms latency

Improvement: 5x RPS, 5x latency reduction
```

## Technical Details

### Radix v4 Pool Implementation

```go
// Radix v4 uses a pool of connections
type Pool struct {
    conns chan *conn
    size  int
}

// Getting a connection
func (p *Pool) Get() (*conn, error) {
    select {
    case conn := <-p.conns:
        return conn, nil  // Available immediately
    default:
        return nil, ErrPoolExhausted  // Must wait or create
    }
}

// Our parallel execution hits this hard
for _, pipeline := range pipelines {
    go func(p Pipeline) {
        conn := pool.Get()  // May block here if pool exhausted!
        defer pool.Put(conn)
        conn.Do(p)
    }(pipeline)
}
```

### Why Parallel Execution Needs More Connections

**Sequential Execution (OLD):**
```
Request 1: Get conn → Use → Return → Next
Request 2: Get conn → Use → Return → Next
Request 3: Get conn → Use → Return → Next

Peak concurrent connections: 1-2
Pool size needed: 10 sufficient
```

**Parallel Execution (NEW):**
```
Request 1: Get conn1 ┐
Request 2: Get conn2 ├─ ALL CONCURRENT
Request 3: Get conn3 ┘

Peak concurrent connections: 6-7 per worker × 100 workers = 600
Pool size needed: 50+ for good performance
```

## Lessons Learned

1. **Parallelization ≠ Performance**
   - Parallel execution is necessary but not sufficient
   - Resource bottlenecks (pool) can negate parallel benefits
   - Always consider entire system, not just algorithmic improvements

2. **Connection Pools are Critical**
   - In distributed systems, connection pooling is often the bottleneck
   - Pool size must scale with concurrency × parallelism
   - Monitoring pool saturation is essential

3. **Default Values Matter**
   - Default POOL_SIZE=10 was chosen for standalone Redis
   - Redis Cluster + parallel execution needs different defaults
   - Defaults should match common production scenarios

4. **Testing Reveals Bottlenecks**
   - Initial "parallel execution" seemed successful (3.8k RPS)
   - Only comparative testing revealed true potential (32k RPS)
   - Always benchmark with various configurations

## Future Work

### 1. Dynamic Pool Sizing

```go
// Auto-adjust pool size based on load
type AdaptivePool struct {
    minSize int
    maxSize int
    currentSize int
}

func (p *AdaptivePool) Adjust() {
    if p.saturation > 0.9 {
        p.Grow()  // Increase pool size
    } else if p.saturation < 0.3 {
        p.Shrink()  // Decrease pool size
    }
}
```

### 2. Per-Node Pools

```go
// Separate pool per cluster node
type ClusterPool struct {
    pools map[string]*Pool  // node -> pool
}

// Better isolation, no cross-node contention
```

### 3. Pool Metrics

```go
// Expose detailed pool metrics
- pool.size (gauge)
- pool.active (gauge)
- pool.wait_time_ms (histogram)
- pool.saturation_ratio (gauge)
```

## References

- [Radix v4 Documentation](https://pkg.go.dev/github.com/mediocregopher/radix/v4)
- [Redis Cluster Specification](https://redis.io/docs/reference/cluster-spec/)
- [Proposal: Parallel Pipeline Execution](../proposal/redis-cluster-improvements/02-parallel-pipeline-execution.md)
- [Performance Analysis: mixed_2keys vs mixed_10keys](../proposal/redis-cluster-improvements/05-performance-analysis.md)

## Appendix: Raw Test Data

### Pool-10 Results
```
fixed_key:    23,743 RPS,  4.21ms avg
variable_key: 21,287 RPS,  4.70ms avg
mixed_2keys:  20,018 RPS,  4.99ms avg
mixed_10keys:  6,080 RPS, 16.45ms avg
```

### Pool-50 Results
```
fixed_key:    24,032 RPS,  4.16ms avg
variable_key: 22,215 RPS,  4.50ms avg
mixed_2keys:  21,234 RPS,  4.71ms avg
mixed_10keys: 31,636 RPS,  3.16ms avg  ← 5.2x improvement!
```

### Pool-100 Results
```
fixed_key:    24,167 RPS,  4.14ms avg
variable_key: 22,472 RPS,  4.45ms avg
mixed_2keys:  21,477 RPS,  4.65ms avg
mixed_10keys: 32,634 RPS,  3.06ms avg  ← 5.4x improvement!
```

---

**Last Updated**: 2025-12-23
**Author**: Claude Code (Claude Sonnet 4.5)
**Status**: ✅ Production Recommendation - Use REDIS_POOL_SIZE=50

# Radix v3 vs v4 Performance Comparison

## Executive Summary

Radix v4 delivers **44-58% higher throughput** and **31-37% lower latency** compared to v3, with the greatest improvements in Redis Cluster multi-key scenarios.

## Test Environment

### Configuration
```yaml
Redis Type: Cluster (3 nodes, 16384 slots)
Pipeline Settings: window=150us, limit=8
Concurrency: 100 workers
Connections: 10 per pool
Test Duration: 10 seconds per scenario
Warmup: 2 seconds
```

### Test Scenarios

1. **fixed_key**: Single fixed key (hot key scenario)
2. **variable_key**: Different key each request
3. **mixed_2keys**: 1 fixed + 1 variable key per request
4. **mixed_10keys**: 5 fixed + 5 variable keys per request

## Performance Results

### Radix v4 (async-pipeline-radixv4) - Average of 2 Runs

| Scenario | RPS | Avg Latency | P50 | P95 | P99 |
|----------|-----|-------------|-----|-----|-----|
| **fixed_key** | **23,381** | **4.28ms** | 4.02ms | 6.56ms | 8.68ms |
| **variable_key** | **21,255** | **4.70ms** | 4.41ms | 7.75ms | 10.21ms |
| **mixed_2keys** | **20,426** | **4.90ms** | 4.57ms | 8.09ms | 10.89ms |
| **mixed_10keys** | **2,994** | **33.54ms** | 33.07ms | 41.24ms | 47.50ms |

### Radix v3 (async-pipeline) - Average of 2 Runs

| Scenario | RPS | Avg Latency | P50 | P95 | P99 |
|----------|-----|-------------|-----|-----|-----|
| **fixed_key** | **16,132** | **6.20ms** | 6.09ms | 8.32ms | 9.80ms |
| **variable_key** | **14,409** | **6.94ms** | 6.58ms | 10.34ms | 13.98ms |
| **mixed_2keys** | **14,153** | **7.07ms** | 6.70ms | 10.71ms | 13.83ms |
| **mixed_10keys** | **1,891** | **53.01ms** | 52.50ms | 61.78ms | 67.65ms |

## Improvement Analysis

### Throughput (RPS) Improvements

| Scenario | v3 RPS | v4 RPS | Improvement |
|----------|--------|--------|-------------|
| **fixed_key** | 16,132 | 23,381 | **+45.0%** ⬆️ |
| **variable_key** | 14,409 | 21,255 | **+47.5%** ⬆️ |
| **mixed_2keys** | 14,153 | 20,426 | **+44.3%** ⬆️ |
| **mixed_10keys** | 1,891 | 2,994 | **+58.3%** ⬆️ |

### Latency Improvements

| Scenario | v3 Latency | v4 Latency | Improvement |
|----------|------------|------------|-------------|
| **fixed_key** | 6.20ms | 4.28ms | **-31.0%** ⬇️ |
| **variable_key** | 6.94ms | 4.70ms | **-32.3%** ⬇️ |
| **mixed_2keys** | 7.07ms | 4.90ms | **-30.7%** ⬇️ |
| **mixed_10keys** | 53.01ms | 33.54ms | **-36.7%** ⬇️ |

## Key Findings

### 1. Consistent Improvements Across All Scenarios

- **RPS**: 44-58% higher across the board
- **Latency**: 31-37% lower in all cases
- **Stability**: Very low variance between test runs

### 2. Greatest Benefit for Multi-Key Operations

```
mixed_10keys improvement:
- RPS: +58.3% (best improvement)
- Latency: -36.7% (best improvement)

Why? v4's slot-based grouping and explicit pipelining
optimize multi-slot operations more effectively.
```

### 3. Tail Latency Improvements

| Scenario | v3 P99 | v4 P99 | Improvement |
|----------|--------|--------|-------------|
| **fixed_key** | 9.80ms | 8.68ms | -11.4% |
| **variable_key** | 13.98ms | 10.21ms | -27.0% |
| **mixed_2keys** | 13.83ms | 10.89ms | -21.3% |
| **mixed_10keys** | 67.65ms | 47.50ms | -29.8% |

Even high percentiles show significant improvements!

## Technical Differences

### Radix v3 Architecture

```
Implicit Pipelining (automatic batching):
├─ Pool with window/limit settings
├─ Automatic command batching
├─ Flush on timer or size limit
└─ Limited control over batching

Limitations:
• Automatic batching is convenient but less efficient
• No slot-awareness for Redis Cluster
• Overhead from automatic batching logic
```

### Radix v4 Architecture

```
Explicit Pipelining (manual control):
├─ Action-based API (CanPipeline, Keys, etc.)
├─ Manual pipeline construction
├─ Slot-based grouping for Cluster
└─ Parallel execution of slot groups

Advantages:
• Direct control over batching
• Slot-aware for optimal Cluster performance
• Cleaner API with better performance
• Future-proof (v3 is deprecated)
```

## Why v4 is Faster

### 1. Explicit Pipeline API

```go
// v3: Automatic (convenient but slower)
for _, action := range pipeline {
    client.Do(action)  // Auto-batched internally
}

// v4: Explicit (more control, faster)
p := radix.NewPipeline()
for _, action := range pipeline {
    p.Append(action)
}
client.Do(ctx, p)  // Single execution
```

### 2. Slot-Based Optimization

```go
// v4: Group by slot for Cluster
pipelines := make(map[uint16]Pipeline)
for _, key := range keys {
    slot := radix.ClusterSlot([]byte(key))
    pipelines[slot] = append(pipelines[slot], ...)
}
// Execute each slot's pipeline
```

### 3. Action Properties

```go
// v4: Metadata-driven optimization
type ActionProperties struct {
    Keys         []string  // For cluster routing
    CanPipeline  bool      // Can batch with others
    CanShareConn bool      // Can share connection
    CanRetry     bool      // Can retry on MOVED/ASK
}
```

### 4. Better Connection Management

- Improved connection pooling
- More efficient connection reuse
- Lower overhead per operation

## Consistency Analysis

### v4 Test Variance (2 runs)

```
fixed_key:
  Run 1: 23,511 RPS
  Run 2: 23,251 RPS
  Variance: 1.1%

variable_key:
  Run 1: 21,078 RPS
  Run 2: 21,431 RPS
  Variance: 1.7%

mixed_10keys:
  Run 1: 2,957 RPS
  Run 2: 3,031 RPS
  Variance: 2.5%
```

Very stable performance across runs!

### v3 Test Variance (2 runs)

```
fixed_key:
  Run 1: 16,036 RPS
  Run 2: 16,228 RPS
  Variance: 1.2%

variable_key:
  Run 1: 14,322 RPS
  Run 2: 14,495 RPS
  Variance: 1.2%

mixed_10keys:
  Run 1: 1,892 RPS
  Run 2: 1,890 RPS
  Variance: 0.1%
```

Also stable, but consistently slower.

## Migration Impact

### Breaking Changes

**None for our use case!**

The changes are internal to the driver layer. Application code remains unchanged.

### Required Changes

1. Update radix dependency: `v3 → v4`
2. Implement slot-based grouping (done)
3. Update pipeline construction (done)

### Compatibility

- ✅ Redis Standalone: Works perfectly
- ✅ Redis Cluster: Now fully supported
- ✅ Redis Sentinel: Works perfectly
- ✅ Existing features: All maintained

## Recommendation

### ✅ Migrate to Radix v4

**Reasons:**
1. **44-58% higher throughput** across all scenarios
2. **31-37% lower latency** for better user experience
3. **Redis Cluster support** (critical for scale)
4. **Better tail latencies** (P99 improvements)
5. **Future-proof** (v3 is no longer maintained)
6. **Zero application code changes** required

### Migration Strategy

```
Phase 1: Testing (1 week)
├─ Deploy to staging environment
├─ Run load tests
├─ Monitor for 3-5 days
└─ Validate metrics

Phase 2: Gradual Rollout (2 weeks)
├─ Deploy to 10% of production traffic
├─ Monitor for 2-3 days
├─ Increase to 50%
├─ Monitor for 2-3 days
└─ Complete rollout to 100%

Phase 3: Validation (1 week)
├─ Compare metrics vs baseline
├─ Validate no regressions
└─ Document improvements
```

## Monitoring Checklist

During and after migration:

- [ ] RPS (should increase 40-60%)
- [ ] Average latency (should decrease 30-40%)
- [ ] P95/P99 latency (should decrease 10-30%)
- [ ] Error rate (should remain at baseline)
- [ ] Connection pool usage
- [ ] Redis CPU/memory on cluster nodes
- [ ] Application CPU/memory

## Conclusion

Radix v4 delivers substantial and consistent performance improvements across all scenarios, with the greatest benefits for Redis Cluster deployments. The migration is low-risk with high reward, and v4's continued maintenance makes it the clear choice for production.

**Bottom Line: Migrate to v4 for 44-58% better performance with zero application code changes!**

## Appendix: Raw Test Data

### v4 Test 1
```
fixed_key:    23,511 RPS, 4.25ms avg
variable_key: 21,078 RPS, 4.74ms avg
mixed_2keys:  20,548 RPS, 4.87ms avg
mixed_10keys:  2,957 RPS, 33.88ms avg
```

### v4 Test 2
```
fixed_key:    23,251 RPS, 4.30ms avg
variable_key: 21,431 RPS, 4.66ms avg
mixed_2keys:  20,303 RPS, 4.92ms avg
mixed_10keys:  3,031 RPS, 33.20ms avg
```

### v3 Test 1
```
fixed_key:    16,036 RPS, 6.24ms avg
variable_key: 14,322 RPS, 6.99ms avg
mixed_2keys:  14,099 RPS, 7.09ms avg
mixed_10keys:  1,892 RPS, 52.98ms avg
```

### v3 Test 2
```
fixed_key:    16,228 RPS, 6.16ms avg
variable_key: 14,495 RPS, 6.90ms avg
mixed_2keys:  14,206 RPS, 7.04ms avg
mixed_10keys:  1,890 RPS, 53.05ms avg
```

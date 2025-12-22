# Performance Test Results - Envoy Ratelimit

**Test Date:** 2025-12-21
**Test Duration:** 10s per scenario
**Concurrency:** 100 workers
**Connections:** 10 gRPC connections
**Warmup:** 2s

---

## Executive Summary

Comprehensive performance testing was conducted across Redis standalone, Redis cluster, and Memcached configurations with various optimization settings. All 10 endpoints were tested successfully with 4 scenarios each (40 total test runs).

### Key Findings

1. **Redis standalone with pipeline 150us** achieves highest fixed-key throughput (11,832 RPS)
2. **Hot key detection** improves variable key performance by ~17% (12,189 vs 10,375 RPS)
3. **Redis cluster** shows comparable performance to standalone with HA benefits
4. **Memcached** provides similar performance with lowest memory footprint (1.81 MB)
5. **10-key scenario** properly tests multi-descriptor rate limiting (~2,000 RPS)

---

## Test Scenarios

| Scenario | Description |
|----------|-------------|
| **fixed_key** | Single fixed key - tests hot key detection effectiveness |
| **variable_key** | Random unique keys - tests general throughput |
| **mixed_2keys** | 2 descriptors (1 fixed + 1 variable) - tests nested rate limits |
| **mixed_10keys** | 10 separate descriptors (5 fixed + 5 variable) - tests multi-key workloads |

---

## Performance Comparison Summary

### Redis Standalone

| Endpoint | Fixed Key | Variable Key | Mixed 2 | Mixed 10 | CPU (cores) | Memory |
|----------|-----------|--------------|---------|----------|-------------|--------|
| **redis_baseline** | 11,394 | 10,375 | 11,085 | 1,927 | 0.01 | 3.51 MB |
| **redis_hotkey_enabled** | 11,354 | **12,189** | 10,913 | 1,945 | 0.01 | 3.51 MB |
| **redis_pipeline_75us** | 10,928 | 11,068 | 10,332 | 1,952 | 0.00 | 3.51 MB |
| **redis_pipeline_150us** | **11,832** | 10,015 | 10,399 | 1,899 | 0.00 | 3.52 MB |
| **redis_hotkey_pipeline** | 10,205 | 10,307 | 10,120 | 1,908 | 0.00 | 3.51 MB |

### Redis Cluster (3 nodes)

| Endpoint | Fixed Key | Variable Key | Mixed 2 | Mixed 10 | CPU (cores) | Memory |
|----------|-----------|--------------|---------|----------|-------------|--------|
| **cluster_baseline** | 10,733 | 10,945 | **11,480** | **2,014** | 0.01 | 204.05 MB |
| **cluster_hotkey** | 10,401 | 11,229 | 10,360 | 1,899 | 0.01 | 207.45 MB |
| **cluster_hotkey_pipeline** | 11,519 | 10,735 | 9,952 | 1,982 | 0.02 | 208.72 MB |

### Memcached

| Endpoint | Fixed Key | Variable Key | Mixed 2 | Mixed 10 | CPU (cores) | Memory |
|----------|-----------|--------------|---------|----------|-------------|--------|
| **memcached_baseline** | 10,829 | 10,305 | 10,783 | 1,997 | 0.00 | 1.81 MB |
| **memcached_hotkey** | 11,474 | 11,129 | 9,613 | 1,986 | 0.00 | 1.81 MB |

---

## Best Performers

| Scenario | Best Configuration | RPS |
|----------|-------------------|-----|
| **fixed_key** | redis_pipeline_150us | 11,832 |
| **variable_key** | redis_hotkey_enabled | 12,189 |
| **mixed_2keys** | cluster_baseline | 11,480 |
| **mixed_10keys** | cluster_baseline | 2,014 |

---

## Detailed Latency Analysis

### Redis Baseline (No Optimization)

| Scenario | Avg | P50 | P95 | P99 | P99.9 | Max |
|----------|-----|-----|-----|-----|-------|-----|
| fixed_key | 8.77ms | 8.39ms | 13.02ms | 17.52ms | 33.64ms | 46.41ms |
| variable_key | 9.64ms | 8.86ms | 14.71ms | 24.32ms | 75.45ms | 120.70ms |
| mixed_2keys | 9.03ms | 8.43ms | 14.16ms | 18.99ms | 37.74ms | 47.96ms |
| mixed_10keys | 51.98ms | 49.06ms | 69.53ms | 103.55ms | 183.50ms | 199.30ms |

### Redis with Hot Key Detection

| Scenario | Avg | P50 | P95 | P99 | P99.9 | Max |
|----------|-----|-----|-----|-----|-------|-----|
| fixed_key | 8.81ms | 8.41ms | 13.26ms | 17.61ms | 31.28ms | 54.13ms |
| variable_key | 8.21ms | 7.86ms | 11.92ms | 15.78ms | 24.06ms | 36.10ms |
| mixed_2keys | 9.17ms | 8.60ms | 14.27ms | 19.72ms | 37.44ms | 67.49ms |
| mixed_10keys | 51.55ms | 49.93ms | 66.27ms | 89.34ms | 118.68ms | 127.09ms |

### Redis with Pipeline 150us

| Scenario | Avg | P50 | P95 | P99 | P99.9 | Max |
|----------|-----|-----|-----|-----|-------|-----|
| fixed_key | 8.45ms | 8.17ms | 12.38ms | 15.92ms | 21.71ms | 35.08ms |
| variable_key | 9.99ms | 9.16ms | 15.47ms | 22.35ms | 89.94ms | 121.17ms |
| mixed_2keys | 9.62ms | 8.95ms | 14.88ms | 22.87ms | 59.80ms | 79.30ms |
| mixed_10keys | 52.77ms | 50.00ms | 73.44ms | 111.28ms | 155.57ms | 164.81ms |

### Redis Cluster Baseline

| Scenario | Avg | P50 | P95 | P99 | P99.9 | Max |
|----------|-----|-----|-----|-----|-------|-----|
| fixed_key | 9.32ms | 8.93ms | 13.85ms | 17.56ms | 30.54ms | 58.05ms |
| variable_key | 9.14ms | 8.66ms | 13.78ms | 16.96ms | 28.94ms | 63.40ms |
| mixed_2keys | 8.71ms | 8.23ms | 13.31ms | 17.14ms | 23.64ms | 30.02ms |
| mixed_10keys | 49.81ms | 48.04ms | 64.67ms | 88.31ms | 116.97ms | 130.61ms |

### Memcached Baseline

| Scenario | Avg | P50 | P95 | P99 | P99.9 | Max |
|----------|-----|-----|-----|-----|-------|-----|
| fixed_key | 9.24ms | 8.89ms | 13.62ms | 17.32ms | 30.39ms | 74.24ms |
| variable_key | 9.70ms | 9.02ms | 15.28ms | 23.39ms | 40.15ms | 61.45ms |
| mixed_2keys | 9.28ms | 8.82ms | 14.16ms | 18.03ms | 23.48ms | 31.57ms |
| mixed_10keys | 50.21ms | 48.51ms | 65.23ms | 78.55ms | 96.83ms | 105.28ms |

### Memcached with Hot Key Detection

| Scenario | Avg | P50 | P95 | P99 | P99.9 | Max |
|----------|-----|-----|-----|-----|-------|-----|
| fixed_key | 8.72ms | 8.44ms | 12.95ms | 15.93ms | 21.13ms | 31.16ms |
| variable_key | 8.99ms | 8.47ms | 13.49ms | 17.64ms | 58.41ms | 75.42ms |
| mixed_2keys | 10.41ms | 9.67ms | 16.55ms | 24.39ms | 45.81ms | 63.50ms |
| mixed_10keys | 50.49ms | 49.57ms | 61.69ms | 71.57ms | 103.66ms | 112.05ms |

---

## Resource Usage Comparison

| Backend | Type | CPU (cores) | Memory |
|---------|------|-------------|--------|
| Redis Standalone | Single node | 0.00 - 0.01 | 3.51 MB |
| Redis Cluster | 3 nodes (total) | 0.01 - 0.02 | 204 - 209 MB |
| Memcached | Single node | 0.00 | 1.81 MB |

### Observations

- **Memcached** uses the least memory (1.81 MB) - ideal for memory-constrained environments
- **Redis Standalone** uses minimal resources (3.51 MB) with excellent performance
- **Redis Cluster** has higher memory overhead (~207 MB total for 3 nodes) but provides HA

---

## Configuration Details

### Test Configurations

```yaml
# Redis Baseline
REDIS_PIPELINE_WINDOW: "0"
REDIS_PIPELINE_LIMIT: "0"
HOTKEY_DETECTION_ENABLED: "false"

# Redis Hot Key Enabled
HOTKEY_DETECTION_ENABLED: "true"
HOTKEY_DETECTION_THRESHOLD: "100"
HOTKEY_BATCHING_ENABLED: "true"
HOTKEY_BATCHING_FLUSH_INTERVAL: "300us"

# Redis Pipeline 75us
REDIS_PIPELINE_WINDOW: "75us"
REDIS_PIPELINE_LIMIT: "4"

# Redis Pipeline 150us
REDIS_PIPELINE_WINDOW: "150us"
REDIS_PIPELINE_LIMIT: "8"

# Cluster Configuration
REDIS_TYPE: "cluster"
REDIS_URL: "localhost:7001,localhost:7002,localhost:7003"
```

---

## Performance Visualization

```
Requests Per Second (RPS) - Fixed Key Scenario
==============================================

redis_pipeline_150us      ████████████████████████████████████████ 11,832
cluster_hotkey_pipeline   ██████████████████████████████████████░░ 11,519
memcached_hotkey          ██████████████████████████████████████░░ 11,474
redis_baseline            ██████████████████████████████████████░░ 11,394
redis_hotkey_enabled      ██████████████████████████████████████░░ 11,354
redis_pipeline_75us       █████████████████████████████████████░░░ 10,928
memcached_baseline        ████████████████████████████████████░░░░ 10,829
cluster_baseline          ████████████████████████████████████░░░░ 10,733
cluster_hotkey            ███████████████████████████████████░░░░░ 10,401
redis_hotkey_pipeline     ██████████████████████████████████░░░░░░ 10,205
```

```
Latency P99 (Lower is Better) - Fixed Key Scenario
==================================================

redis_pipeline_150us      ████████░░░░░░░░░░░░░░░░░░░░░░ 15.92ms
memcached_hotkey          ████████░░░░░░░░░░░░░░░░░░░░░░ 15.93ms
redis_pipeline_75us       ████████░░░░░░░░░░░░░░░░░░░░░░ 16.22ms
memcached_baseline        █████████░░░░░░░░░░░░░░░░░░░░░ 17.32ms
cluster_hotkey_pipeline   █████████░░░░░░░░░░░░░░░░░░░░░ 17.50ms
redis_baseline            █████████░░░░░░░░░░░░░░░░░░░░░ 17.52ms
cluster_baseline          █████████░░░░░░░░░░░░░░░░░░░░░ 17.56ms
redis_hotkey_enabled      █████████░░░░░░░░░░░░░░░░░░░░░ 17.61ms
cluster_hotkey            ████████████░░░░░░░░░░░░░░░░░░ 23.19ms
redis_hotkey_pipeline     █████████████░░░░░░░░░░░░░░░░░ 24.62ms
```

---

## Recommendations

### For Maximum Throughput (Fixed Key Workloads)
```bash
REDIS_PIPELINE_WINDOW=150us
REDIS_PIPELINE_LIMIT=8
```
Expected: ~11,800 RPS with 8.45ms avg latency

### For Variable Key Workloads
```bash
HOTKEY_DETECTION_ENABLED=true
HOTKEY_DETECTION_THRESHOLD=100
HOTKEY_BATCHING_ENABLED=true
```
Expected: ~12,200 RPS with 8.21ms avg latency

### For High Availability Requirements
```bash
REDIS_TYPE=cluster
REDIS_URL=node1:7001,node2:7002,node3:7003
```
Expected: ~10,700-11,500 RPS with automatic failover

### For Memory-Constrained Environments
```bash
BACKEND_TYPE=memcache
MEMCACHE_HOST_PORT=localhost:11211
```
Expected: ~10,800 RPS with only 1.81 MB memory usage

---

## Test Environment

- **OS:** macOS (Darwin 25.1.0)
- **Go Version:** 1.23.9
- **Redis Version:** 7 (Alpine)
- **Memcached Version:** 1.6 (Alpine)
- **Docker:** Docker Desktop for Mac

---

## Reproducing These Results

```bash
# Start all backends with monitoring
docker compose -f docker-compose-perf.yml up -d

# Run full test suite with metrics
./scripts/run-perf-test.sh -e test/perf/endpoints.yaml -m

# Run specific scenario
./scripts/run-perf-test.sh -e test/perf/endpoints.yaml -s fixed -m

# View results
cat test/perf/results/results_*.json | python3 -m json.tool
```

---

## Raw Data

Full JSON results available at: `test/perf/results/results_20251221_233157.json`

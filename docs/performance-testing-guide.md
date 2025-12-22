# Performance Testing Guide

This guide explains how to run gRPC performance tests for the Envoy Ratelimit service.

## Overview

The performance test suite measures:
- **Requests per second (RPS)**: Throughput capacity
- **Latency percentiles**: p50, p75, p90, p95, p99, p99.9
- **Error rates**: Failed requests during the test

### Test Scenarios

| Scenario | Description | Use Case |
|----------|-------------|----------|
| `fixed` | Same key for every request | Hot key detection effectiveness |
| `variable` | Different key for each request | Unique key handling performance |
| `mixed` | Fixed key + variable key combined | Real-world mixed workload |

## Quick Start

```bash
# Simple test with defaults (requires Redis on localhost:6379)
./scripts/run-perf-test.sh

# Run specific scenario
./scripts/run-perf-test.sh -s fixed

# Multi-endpoint comparison test
./scripts/run-perf-test.sh -e test/perf/endpoints.yaml
```

## Prerequisites

### Required
- Go 1.23+
- Redis (standalone or cluster)
- Python 3 with PyYAML (`pip install pyyaml`)

### Optional
- Memcached (for memcached backend testing)
- Docker (for easy backend setup)

### Starting Backends with Docker

```bash
# Redis standalone
docker run -d --name redis -p 6379:6379 redis:alpine

# Redis cluster (3 nodes)
docker run -d --name redis-cluster -p 7001-7003:7001-7003 \
  grokzen/redis-cluster:latest

# Memcached
docker run -d --name memcached -p 11211:11211 memcached:alpine
```

## Command Line Options

```
Usage: ./scripts/run-perf-test.sh [OPTIONS]

Options:
  -c, --concurrency NUM    Number of concurrent workers (default: 100)
  -d, --duration DURATION  Test duration (default: 10s)
  -n, --connections NUM    Number of gRPC connections (default: 10)
  -w, --warmup DURATION    Warmup duration (default: 2s)
  -s, --scenario SCENARIO  Test scenario: fixed, variable, mixed, all (default: all)
  -r, --redis URL          Redis URL (default: localhost:6379)
  -p, --port PORT          gRPC port (default: 8081)
  -l, --log-level LEVEL    Log level: debug, info, warn, error (default: error)
  -e, --endpoints FILE     Endpoints configuration file for multi-endpoint testing
  --skip-build             Skip building binaries
  --skip-server            Don't start server (use existing)
  -h, --help               Show help
```

## Single Endpoint Testing

### Basic Usage

```bash
# Run all scenarios with 100 concurrent workers for 10 seconds
./scripts/run-perf-test.sh

# High concurrency test
./scripts/run-perf-test.sh -c 500 -d 30s

# Test only fixed key scenario
./scripts/run-perf-test.sh -s fixed -d 20s

# Use existing server (don't restart)
./scripts/run-perf-test.sh --skip-server --skip-build
```

### Example Output

```
================================================================================
  Scenario: fixed_key
================================================================================

  Summary:
    Total Requests:  43982
    Successful:      43882
    Errors:          0
    Duration:        10s
    Requests/sec:    4398.04

  Latency Distribution:
    Metric          Value
    ------          -----
    min           3.502ms
    avg          22.759ms
    p50          21.255ms
    p75          23.671ms
    p90          27.199ms
    p95          31.116ms
    p99          55.073ms
    p999         88.051ms
    max          91.618ms
```

## Multi-Endpoint Testing

Multi-endpoint testing allows you to compare different backend configurations automatically.

### Configuration File Format

Create a YAML file (e.g., `test/perf/endpoints.yaml`):

```yaml
# Global test settings
test_settings:
  concurrency: 100
  duration: 10s
  warmup: 2s
  connections: 10

# Endpoint configurations
endpoints:
  # Redis standalone baseline
  - name: "redis-standalone"
    type: redis
    settings:
      REDIS_SOCKET_TYPE: tcp
      REDIS_TYPE: SINGLE
      REDIS_URL: localhost:6379
      REDIS_POOL_SIZE: 10
      HOT_KEY_DETECTION_ENABLED: "false"

  # Redis with hot key detection
  - name: "redis-hotkey"
    type: redis
    settings:
      REDIS_SOCKET_TYPE: tcp
      REDIS_TYPE: SINGLE
      REDIS_URL: localhost:6379
      REDIS_POOL_SIZE: 10
      HOT_KEY_DETECTION_ENABLED: "true"
      HOT_KEY_THRESHOLD: 100
      HOT_KEY_FLUSH_WINDOW: "100us"

  # Redis cluster (pipeline required)
  - name: "redis-cluster"
    type: redis
    settings:
      REDIS_SOCKET_TYPE: tcp
      REDIS_TYPE: CLUSTER
      REDIS_URL: localhost:7001
      REDIS_POOL_SIZE: 10
      REDIS_PIPELINE_WINDOW: "150us"
      REDIS_PIPELINE_LIMIT: 8

  # Memcached
  - name: "memcached"
    type: memcached
    settings:
      BACKEND_TYPE: memcache
      MEMCACHE_HOST_PORT: localhost:11211
      MEMCACHE_MAX_IDLE_CONNS: 10
```

### Running Multi-Endpoint Tests

```bash
# Run with configuration file
./scripts/run-perf-test.sh -e test/perf/endpoints.yaml

# Override scenario
./scripts/run-perf-test.sh -e test/perf/endpoints.yaml -s fixed
```

### Comparison Output

```
====================================================================================================
  Performance Comparison
====================================================================================================

Endpoint                  Scenario               RPS        Avg        P50        P95        P99      P99.9
----------------------------------------------------------------------------------------------------
redis-standalone          fixed_key             4292    23.32ms    21.66ms    31.93ms    51.10ms   129.91ms
redis-standalone-hotkey   fixed_key            27136     3.68ms     3.29ms     5.92ms    10.68ms    36.11ms
redis-cluster             fixed_key            12300     8.13ms     7.80ms    11.88ms    16.25ms    26.13ms

====================================================================================================
  Best Performers (by RPS)
====================================================================================================

  fixed_key: redis-standalone-hotkey (27136 RPS)
```

## Available Settings

### Redis Settings

| Setting | Description | Default |
|---------|-------------|---------|
| `REDIS_URL` | Redis server address | localhost:6379 |
| `REDIS_TYPE` | SINGLE, CLUSTER, or SENTINEL | SINGLE |
| `REDIS_POOL_SIZE` | Connection pool size | 10 |
| `REDIS_PIPELINE_WINDOW` | Pipeline batch window | 0 (disabled) |
| `REDIS_PIPELINE_LIMIT` | Max commands per pipeline | 0 (disabled) |
| `REDIS_TIMEOUT` | Operation timeout | 10s |

### Hot Key Detection Settings

| Setting | Description | Default |
|---------|-------------|---------|
| `HOT_KEY_DETECTION_ENABLED` | Enable hot key detection | false |
| `HOT_KEY_THRESHOLD` | Frequency threshold for hot keys | 100 |
| `HOT_KEY_FLUSH_WINDOW` | Batch flush interval | 100us |
| `HOT_KEY_MAX_COUNT` | Max hot keys to track | 10000 |

### Memcached Settings

| Setting | Description | Default |
|---------|-------------|---------|
| `BACKEND_TYPE` | Must be "memcache" | redis |
| `MEMCACHE_HOST_PORT` | Memcached server address | - |
| `MEMCACHE_MAX_IDLE_CONNS` | Max idle connections | 2 |

## Performance Test Client

The test client can also be used directly:

```bash
# Build the client
go build -o bin/perf_test ./test/perf/perf_client.go

# Run directly
./bin/perf_test -addr localhost:8081 -c 100 -d 10s -scenario all

# Output as JSON
./bin/perf_test -addr localhost:8081 -json results.json

# Quiet mode (JSON only)
./bin/perf_test -addr localhost:8081 -json results.json -q
```

### Client Options

```
  -addr string        gRPC server address (default "localhost:8081")
  -c int              Number of concurrent workers (default 100)
  -conn int           Number of gRPC connections (default 10)
  -d duration         Test duration (default 10s)
  -warmup duration    Warmup duration (default 2s)
  -scenario string    Test scenario: fixed, variable, mixed, all (default "all")
  -endpoint string    Endpoint name for labeling results
  -json string        Output results as JSON to file (use '-' for stdout)
  -q                  Quiet mode - only output JSON (requires -json)
```

## Rate Limit Configuration

The test uses a configuration file at `test/perf/config/config.yaml`:

```yaml
domain: perf_test
descriptors:
  # Fixed key - for hot key scenario
  - key: api_key
    value: fixed_key
    rate_limit:
      unit: second
      requests_per_unit: 100000

  # Variable key - matches any api_key value
  - key: api_key
    rate_limit:
      unit: second
      requests_per_unit: 100000

  # Mixed scenario - fixed + variable
  - key: mixed_test
    value: fixed_part
    descriptors:
      - key: request_id
        rate_limit:
          unit: second
          requests_per_unit: 100000
```

### Descriptor Matching

- Exact match (`api_key: fixed_key`) takes priority
- Default match (`api_key` without value) is fallback
- Only one rule is applied per request (no double Redis queries)

## Results Storage

Test results are saved as JSON in `test/perf/results/`:

```
test/perf/results/
├── results_20251221_175707.json
├── results_20251221_180517.json
└── ...
```

### JSON Result Format

```json
[
  {
    "endpoint": "redis-standalone",
    "scenario": "fixed_key",
    "total_requests": 43982,
    "success_count": 43882,
    "error_count": 0,
    "duration_ms": 10000,
    "rps": 4398.04,
    "latencies": {
      "min_us": 3502,
      "avg_us": 22759,
      "p50_us": 21255,
      "p75_us": 23671,
      "p90_us": 27199,
      "p95_us": 31116,
      "p99_us": 55073,
      "p999_us": 88051,
      "max_us": 91618
    }
  }
]
```

## Benchmarking Tips

### For Accurate Results

1. **Warmup**: Always use warmup period to stabilize connections
2. **Duration**: Run for at least 10-30 seconds for stable metrics
3. **Multiple runs**: Run tests multiple times and average results
4. **Isolated environment**: Minimize other workloads during testing

### Tuning Parameters

```bash
# High throughput test
./scripts/run-perf-test.sh -c 500 -n 50 -d 30s

# Low latency test
./scripts/run-perf-test.sh -c 50 -n 20 -d 30s

# Stress test
./scripts/run-perf-test.sh -c 1000 -n 100 -d 60s
```

### Redis Cluster Requirements

Redis Cluster requires pipeline enabled:

```yaml
settings:
  REDIS_TYPE: CLUSTER
  REDIS_PIPELINE_WINDOW: "150us"  # Required for cluster
  REDIS_PIPELINE_LIMIT: 8
```

## Troubleshooting

### Server fails to start

Check log file:
```bash
cat /tmp/ratelimit-perf-*.log
```

### Connection refused

Verify backend is running:
```bash
nc -z localhost 6379 && echo "Redis OK"
nc -z localhost 7001 && echo "Redis Cluster OK"
nc -z localhost 11211 && echo "Memcached OK"
```

### Low performance

- Increase connection pool size
- Enable pipeline for Redis Cluster
- Enable hot key detection for fixed key workloads
- Check Redis/Memcached resource usage

## Sample Configurations

### Quick Test (5 seconds per scenario)

```yaml
test_settings:
  concurrency: 50
  duration: 5s
  warmup: 1s
  connections: 10
```

### Production-like Test

```yaml
test_settings:
  concurrency: 200
  duration: 60s
  warmup: 10s
  connections: 50
```

### Stress Test

```yaml
test_settings:
  concurrency: 1000
  duration: 120s
  warmup: 30s
  connections: 100
```

# Claude Code Context: Envoy Ratelimit Project

**Purpose**: This document provides essential context for Claude Code sessions to minimize token usage and improve response quality.

**Usage**: At the start of a new session, reference this file to quickly understand the project without extensive exploration.

---

## Quick Start for Claude

When starting a new task in this project, read this file first. It contains all essential context about the codebase structure, recent changes, and development patterns.

---

## Project Identity

**Name**: Envoy Ratelimit
**Type**: Go-based gRPC/HTTP rate limiting service
**Primary Use**: Works with Envoy Proxy for distributed rate limiting
**Backend**: Redis (primary) or Memcache
**Repository Path**: /Users/seonghyun/GolandProjects/ratelimit
**Main Branch**: main
**Current Branch**: async-pipeline

---

## Technology Stack Summary

- **Language**: Go 1.23.9
- **Protocol**: gRPC (Envoy RLS API v3)
- **Cache Backend**: Redis (radix v3.8.1) or Memcache
- **Config Management**: File-based (goruntime) or xDS (Envoy control plane)
- **Observability**: OpenTelemetry, Prometheus, StatsD/DogStatsD
- **Testing**: testify, gomock, miniredis
- **Deployment**: Docker (distroless containers)

---

## Key File Locations

### Critical Source Files (Start Here)

```
src/service/ratelimit.go          # Main service implementation (418 lines)
src/redis/fixed_cache_impl.go     # Rate limiting logic
src/settings/settings.go           # All environment variables (321 lines)
src/service_cmd/runner/runner.go  # Component initialization (202 lines)
src/config/config_impl.go          # YAML config parser
```

### Entry Points

```
src/service_cmd/main.go            # Main service entry
src/client_cmd/main.go             # Test client CLI
src/config_check_cmd/main.go       # Config validator
```

### Recent Advanced Features

```
src/redis/hotkey_detector.go       # Hot key detection (246 lines, NEW)
src/redis/hotkey_batcher.go        # Request batching (228 lines, NEW)
src/redis/countmin_sketch.go       # Probabilistic counting (153 lines, NEW)
```

### Documentation

```
README.md                          # Main docs (1,468 lines)
HOTKEY.md                          # Hot key feature guide
REDIS-CLUSTER-SETUP.md             # Redis cluster setup
docs/project-structure.md          # Detailed structure (this was just created)
docs/hotkey-analysis.md            # Performance analysis
docs/stop-cache-key-increment-guide.md  # DDoS protection
```

### Configuration

```
examples/ratelimit/                # Example rate limit configs
test/integration/runtime/          # Test configurations
docker-compose.yml                 # Local development
docker-compose-cluster.yml         # Redis cluster (NEW)
```

### Testing

```
test/redis/bench_test.go           # Performance benchmarks
test/integration/integration_test.go  # Integration tests
test/mocks/                        # Generated mocks
```

---

## Architecture Overview

```
Request Flow:
Client → gRPC/HTTP Server → Service Handler → Limiter → Hot Key Detector → Batcher → Redis

Components:
1. Service Layer (src/service/): Handles RPC calls, shadow mode, headers
2. Limiter (src/limiter/): Generates cache keys, checks limits
3. Redis Backend (src/redis/): Manages connections, hot keys, batching
4. Config System (src/config/): Parses YAML or xDS configurations
5. Stats (src/stats/): Prometheus/StatsD metrics
6. Tracing (src/trace/): OpenTelemetry integration
```

---

## Code Organization Patterns

### Package Structure
```
src/
├── service_cmd/    # Binary entry points
├── service/        # Business logic layer
├── limiter/        # Rate limiting algorithms
├── redis/          # Redis backend (advanced features)
├── memcached/      # Memcache backend
├── config/         # Configuration parsing
├── provider/       # Config providers (file/xDS)
├── settings/       # Environment variable management
├── server/         # gRPC/HTTP servers
├── stats/          # Metrics collection
├── trace/          # Distributed tracing
└── utils/          # Common utilities
```

### Naming Conventions
- Interfaces end with interface name: `TimeSource`, `RateLimitCache`
- Implementations end with `_impl.go`: `cache_impl.go`, `driver_impl.go`
- Tests end with `_test.go`
- Mocks in `test/mocks/`
- Proto files in `api/ratelimit/`

### Configuration Pattern
- All env vars defined in `src/settings/settings.go`
- Naming: `REDIS_*`, `USE_STATSD`, `RUNTIME_*`, etc.
- Defaults set in settings initialization
- 100+ configurable options

---

## Recent Development Focus (Last 5 Commits)

### 1. Hot Key Detection (9b0ef43) - MOST RECENT
**What**: Count-Min Sketch algorithm for detecting frequently accessed keys
**Why**: Reduce Redis load by batching hot key requests
**Impact**: 75% write reduction, 55% CPU reduction
**Files Changed**:
- src/redis/hotkey_detector.go (NEW)
- src/redis/hotkey_batcher.go (NEW)
- src/redis/countmin_sketch.go (NEW)
- src/settings/settings.go (7 new env vars)

**Key Env Vars**:
- HOTKEY_DETECTION_ENABLED (default: true)
- HOTKEY_DETECTION_THRESHOLD (default: 100)
- HOTKEY_BATCHING_ENABLED (default: true)
- HOTKEY_BATCHING_FLUSH_INTERVAL (default: 300μs)

### 2. Response Dynamic Metadata (167d0f8)
**What**: Support for returning dynamic metadata in responses
**Where**: src/service/ratelimit.go
**Lines Added**: 90+

### 3. Redis Sentinel TLS/Auth Fix (f52a616)
**What**: Separate TLS and auth config for Sentinel vs master/replica
**Where**: src/redis/driver_impl.go

### 4. Pool On-Empty Behavior (fc44670)
**What**: Configurable connection pool exhaustion handling (CREATE/ERROR/WAIT)
**Why**: Prevent connection storms during failures
**Env Vars**: REDIS_POOL_ON_EMPTY_BEHAVIOR, REDIS_POOL_ON_EMPTY_WAIT_DURATION

### 5. Wildcard Stats Fix (e4ac90e)
**What**: Fixed wildcard stats key behavior
**Where**: Limiter and stats components

---

## Current Uncommitted Changes (async-pipeline branch)

**Modified Files**:
- src/redis/driver_impl.go
- src/settings/settings.go
- test/common/common.go
- test/integration/integration_test.go
- test/integration/runtime/current/ratelimit/config/another.yaml
- test/redis/bench_test.go

**New Untracked Files**:
- REDIS-CLUSTER-SETUP.md
- docker-compose-cluster.yml
- docs/ (multiple files)
- monitoring/ (Grafana/Prometheus)
- scripts/ (cluster management)

**Indicates Work On**:
1. Redis cluster support
2. Performance benchmarking improvements
3. Documentation expansion
4. Monitoring infrastructure

---

## Common Development Tasks

### Building
```bash
make compile              # Build all binaries
make tests                # Run all tests
make tests_unit           # Unit tests only
make tests_with_redis     # Tests requiring Redis
make docker_image         # Build Docker image
```

### Testing
```bash
# Run specific test
go test ./src/redis -run TestHotKeyDetector

# Run with race detection
go test -race ./...

# Run benchmarks
go test -bench=. ./test/redis/
```

### Configuration Changes
1. Update `src/settings/settings.go` for new env vars
2. Update `README.md` configuration section
3. Add example to `examples/ratelimit/`
4. Add integration test in `test/integration/`

### Adding New Features
1. Create implementation in appropriate `src/` package
2. Add tests in `test/` package
3. Update `src/settings/settings.go` if config needed
4. Add integration test
5. Update documentation

---

## Important Design Patterns

### 1. Fixed Window Rate Limiting
**Location**: src/redis/fixed_cache_impl.go
**Algorithm**: INCRBY with EXPIRE
**Cache Key Format**: `domain_key_value_value_perUnit_unit`

### 2. Hot Key Optimization
**Location**: src/redis/hotkey_*.go
**Pattern**: Count-Min Sketch → Threshold Detection → Batching → Redis
**Batching Window**: 300μs (configurable)

### 3. Configuration Hierarchy
**Order**: Domain → Descriptors → Nested Descriptors
**Matching**: Exact match → Wildcard → Default
**Merging**: Multiple config files merged by domain

### 4. Shadow Mode
**Purpose**: Test limits without enforcement
**Levels**: Global (SHADOW_MODE=true) or per-descriptor
**Behavior**: Increment counters but never return over-limit

### 5. Local Cache
**Implementation**: freecache (in-memory LRU)
**Purpose**: Cache over-limit decisions to reduce Redis queries
**Config**: LOCAL_CACHE_SIZE_IN_BYTES

---

## Performance Considerations

### Hot Key Feature Impact
- **Write Reduction**: 75% for hot keys
- **CPU Reduction**: 55% under high load
- **Memory**: 10MB for Count-Min Sketch
- **Latency**: <1μs for sketch operations

### Redis Connection Pooling
- Separate pools for per-second and regular limits
- Configurable pool size: REDIS_POOL_SIZE (default: 10)
- Pipeline support for bulk operations
- Health checking enabled

### Optimization Flags
- `STOP_CACHE_KEY_INCREMENT_WHEN_OVERLIMIT`: Prevent increments when over limit (DDoS protection)
- `REDIS_PIPELINE_LIMIT`: Batch Redis operations
- `LOCAL_CACHE_SIZE_IN_BYTES`: Enable local caching

---

## Testing Strategy

### Unit Tests
- Mock Redis using miniredis
- Test individual components in isolation
- Located in `test/` mirroring `src/` structure

### Integration Tests
- Require `-tags=integration`
- Use real Redis instances
- Test full request flow
- Located in `test/integration/`

### Benchmarks
- Located in `test/redis/bench_test.go`
- Test parallel throughput
- Measure Redis CPU impact
- Compare with/without hot key detection

---

## Common Gotchas

1. **Settings Changes**: Always update both `settings.go` and README.md
2. **Redis Cluster**: Different connection pattern than standalone
3. **Wildcards**: Wildcard descriptors (*) require special handling in stats
4. **Shadow Mode**: Doesn't prevent counter increments, only over-limit responses
5. **Local Cache**: Only caches over-limit decisions, not under-limit
6. **Hot Key Detection**: Enabled by default, can affect behavior
7. **Per-Second Limits**: Use separate Redis connection pool

---

## Debugging Tips

### Enable Debug Logging
```bash
LOG_LEVEL=debug ./bin/ratelimit
```

### Check Redis Commands
```bash
redis-cli monitor
```

### Verify Configuration Loading
```bash
./bin/config_check -config_dir examples/ratelimit/config
```

### Test Client
```bash
./bin/client -domain test -descriptor key:value
```

### Hot Key Detection Stats
Look for metrics:
- `ratelimit.redis.hotkey_detected`
- `ratelimit.redis.batched_requests`
- `ratelimit.redis.batch_flush`

---

## Metrics to Monitor

### Key Metrics
- `ratelimit.service.rate_limit.total_hits`: Total requests
- `ratelimit.service.rate_limit.over_limit`: Over-limit responses
- `ratelimit.service.rate_limit.near_limit`: Near-limit responses
- `ratelimit.redis.pipeline_latency`: Redis operation latency
- `ratelimit.redis.hotkey_detected`: Hot keys detected
- `ratelimit.redis.batched_requests`: Batched request count

### Monitoring Stack
- **Location**: monitoring/
- **Grafana**: Pre-configured dashboards
- **Prometheus**: Metrics scraping
- **Redis Exporter**: Per-node Redis metrics

---

## Configuration Examples

### Basic Rate Limit
```yaml
domain: example
descriptors:
  - key: user_id
    rate_limit:
      unit: minute
      requests_per_unit: 100
```

### Wildcard with Share Threshold
```yaml
domain: example
descriptors:
  - key: client_id
    value: "*"
    rate_limit:
      unit: second
      requests_per_unit: 1000
      share_threshold: 100
```

### Nested Descriptors
```yaml
domain: example
descriptors:
  - key: method
    value: POST
    descriptors:
      - key: path
        value: /api/upload
        rate_limit:
          unit: second
          requests_per_unit: 10
```

---

## Environment Variable Quick Reference

### Most Common
```bash
# Server
PORT=8080
GRPC_PORT=8081
HOST=0.0.0.0

# Redis
REDIS_SOCKET_TYPE=tcp
REDIS_URL=localhost:6379
REDIS_POOL_SIZE=10
REDIS_AUTH=password
REDIS_TLS=false

# Configuration
RUNTIME_ROOT=/data
RUNTIME_SUBDIRECTORY=ratelimit
RUNTIME_IGNOREDOTFILES=true

# Stats
USE_STATSD=false
STATSD_HOST=localhost
STATSD_PORT=8125

# Hot Key Detection
HOTKEY_DETECTION_ENABLED=true
HOTKEY_DETECTION_THRESHOLD=100
HOTKEY_BATCHING_ENABLED=true
HOTKEY_BATCHING_FLUSH_INTERVAL=300us

# Performance
LOCAL_CACHE_SIZE_IN_BYTES=0
REDIS_PIPELINE_LIMIT=0
STOP_CACHE_KEY_INCREMENT_WHEN_OVERLIMIT=false
```

Full list: See src/settings/settings.go

---

## API Reference

### gRPC Service
**Proto**: api/ratelimit/config/ratelimit/v3/rls_conf.proto
**Method**: ShouldRateLimit
**Request**: Domain + Descriptors (key-value pairs)
**Response**: OverallCode (OK/OVER_LIMIT) + DescriptorStatuses

### Descriptor Format
```protobuf
message RateLimitDescriptor {
  repeated Entry entries = 1;
  uint32 limit = 2;  // Optional limit override
}

message Entry {
  string key = 1;
  string value = 2;
}
```

---

## Code Quality Standards

### Testing Requirements
- Unit tests for all new features
- Integration tests for Redis/Memcache interaction
- Benchmarks for performance-critical code
- Mocks for external dependencies

### Code Style
- gofmt formatting
- golint compliance
- Pre-commit hooks enabled
- No commented-out code in commits

### Documentation
- Update README.md for new features
- Add godoc comments for public APIs
- Create guides for complex features (like HOTKEY.md)

---

## Deployment Patterns

### Docker
- **Base Image**: gcr.io/distroless/static-debian12:nonroot
- **Multi-arch**: linux/amd64, linux/arm64/v8
- **Registry**: envoyproxy/ratelimit
- **Tags**: Based on git SHA

### Kubernetes
- Use Envoy sidecar or standalone deployment
- Configure Redis connection via env vars
- Mount config directory for file-based config
- Enable health checks: /healthcheck endpoint

### High Availability
- Use Redis Sentinel or Cluster
- Configure REDIS_SENTINEL or REDIS_CLUSTER_NAME
- Enable connection pooling
- Set appropriate timeouts

---

## Troubleshooting Guide

### Service Won't Start
1. Check Redis connectivity
2. Verify configuration directory exists
3. Check RUNTIME_ROOT/RUNTIME_SUBDIRECTORY/RUNTIME_APPDIRECTORY path
4. Enable debug logging

### Rate Limits Not Working
1. Verify descriptor key/value match config
2. Check domain name matches
3. Look for shadow mode settings
4. Check Redis key TTL

### High Redis Load
1. Enable hot key detection
2. Enable local cache
3. Use STOP_CACHE_KEY_INCREMENT_WHEN_OVERLIMIT
4. Check for excessive wildcards
5. Enable pipeline batching

### Memory Issues
1. Check COUNT_MIN_SKETCH_MEMORY_KB setting
2. Review LOCAL_CACHE_SIZE_IN_BYTES
3. Monitor hot key LRU evictions
4. Reduce HOTKEY_LRU_MAX_ITEMS

---

## Links and Resources

- **GitHub**: https://github.com/envoyproxy/ratelimit
- **Envoy RLS API**: https://www.envoyproxy.io/docs/envoy/latest/api-v3/service/ratelimit/v3/rls.proto
- **Envoy Rate Limit Filter**: https://www.envoyproxy.io/docs/envoy/latest/api-v3/extensions/filters/http/ratelimit/v3/rate_limit.proto
- **Docker Hub**: https://hub.docker.com/r/envoyproxy/ratelimit

---

## Quick Command Reference

```bash
# Build
make compile

# Test
make tests
make tests_unit
make tests_with_redis

# Run locally
./bin/ratelimit

# Validate config
./bin/config_check -config_dir path/to/config

# Test with client
./bin/client -domain test -descriptor key:value

# Docker build
make docker_image

# Integration tests
make integration_tests

# Benchmark
cd test/redis && go test -bench=.
```

---

## When to Use This Document

**At Session Start**: Read this file to understand project context
**Before Major Changes**: Review architecture and patterns
**When Adding Features**: Check common development tasks
**When Debugging**: Use troubleshooting guide
**For Configuration**: Reference env var quick reference

This document is meant to be a **living reference** - update it as the project evolves.

---

**Last Updated**: 2025-12-21
**Document Version**: 1.0
**Corresponding Project State**: async-pipeline branch, post-hotkey implementation

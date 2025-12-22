# Envoy Ratelimit Project Structure

Last Updated: 2025-12-21

## Project Overview

**Envoy Ratelimit** is a Go-based generic rate limiting service designed to work with Envoy Proxy and other applications. It provides a gRPC/HTTP service that makes rate limit decisions based on domains and descriptors, using Redis or Memcache as the backend cache.

- **Repository**: /Users/seonghyun/GolandProjects/ratelimit
- **Current Branch**: async-pipeline
- **Main Branch**: main
- **License**: Apache License

## Technology Stack

### Core Technologies

- **Language**: Go 1.23.9
- **Build System**: Go modules
- **Container**: Google distroless (security-hardened)

### Key Dependencies

- **gRPC**: google.golang.org/grpc v1.74.2
- **Envoy Control Plane**: github.com/envoyproxy/go-control-plane v0.13.4
- **Redis Client**: github.com/mediocregopher/radix/v3 v3.8.1
- **Memcache**: github.com/bradfitz/gomemcache
- **Local Cache**: github.com/coocood/freecache v1.2.4
- **Runtime Config**: github.com/lyft/goruntime v0.3.0
- **Stats**: github.com/lyft/gostats v0.4.14

### Observability Stack

- **Tracing**: OpenTelemetry v1.36.0
- **Metrics**: Prometheus v1.19.1
- **DogStatsD**: DataDog v5.5.0

### Testing Stack

- **Framework**: github.com/stretchr/testify v1.10.0
- **Mocking**: github.com/golang/mock v1.6.0
- **Redis Mock**: github.com/alicebob/miniredis/v2 v2.33.0

## Directory Structure

```
/Users/seonghyun/GolandProjects/ratelimit/
├── api/                    # Protocol buffer definitions
│   └── ratelimit/         # RLS API definitions
│       └── config/        # Configuration proto files
├── bin/                    # Compiled binaries
├── docs/                   # Technical documentation
│   ├── hotkey-analysis.md
│   ├── stop-cache-key-increment-guide.md
│   ├── project-structure.md (this file)
│   └── claude-context.md
├── examples/               # Example configurations
│   ├── ratelimit/         # Sample rate limit configs
│   └── docker-compose/    # Docker Compose examples
├── integration-test/       # Integration test suite
├── monitoring/            # Observability stack
│   ├── grafana/          # Grafana dashboards
│   └── prometheus/       # Prometheus configs
├── script/                # Build and utility scripts
├── scripts/               # Cluster management scripts
│   └── redis-cluster/    # Redis cluster setup
├── src/                   # Main source code (detailed below)
└── test/                  # Unit and integration tests
```

## Source Code Structure (`src/`)

Total: ~6,808 lines of Go code

### Core Components

#### `/src/service_cmd/` - Service Entry Point
- **main.go** (11 lines): Minimal entry point
- **runner/runner.go** (202 lines): Component orchestrator
  - Initializes stats, cache, limiter, service
  - Manages component lifecycle
  - Supports multiple stats backends

#### `/src/service/` - Rate Limit Service
- **ratelimit.go** (418 lines): Main service implementation
  - Implements RateLimitServiceServer interface
  - Handles ShouldRateLimit RPC calls
  - Manages configuration updates
  - Supports shadow mode and custom headers
  - Response dynamic metadata support (NEW)

#### `/src/config/` - Configuration System
- **config_impl.go**: YAML configuration parser
- **config_xds.go**: xDS-based configuration provider
- Features:
  - Nested descriptors and wildcards
  - Unlimited limits, shadow mode
  - Detailed metrics, replaces, share_threshold
  - Value-to-metric mapping
  - Domain merging

#### `/src/limiter/` - Rate Limiting Logic
- **base_limiter.go**: Core limiter implementation
- **cache_key.go**: Cache key generation
- Responsibilities:
  - Generate cache keys from descriptors
  - Check over-limit and near-limit thresholds
  - Local cache integration

#### `/src/redis/` - Redis Backend (Advanced)
- **cache_impl.go**: Basic Redis cache
- **fixed_cache_impl.go**: Fixed window rate limiting
- **driver_impl.go**: Redis driver with pooling
- **hotkey_detector.go** (246 lines, NEW): Hot key detection
- **hotkey_batcher.go** (228 lines, NEW): Request batching
- **countmin_sketch.go** (153 lines, NEW): Probabilistic counting

**Advanced Features**:
- Hot Key Detection (Count-Min Sketch)
- 300μs batching window (75% write reduction)
- Pipeline support
- TLS/mTLS support
- Sentinel and Cluster mode
- Per-second and regular pools
- Health checking

#### `/src/settings/` - Settings Management
- **settings.go** (321 lines): Environment configuration
- Manages 100+ configuration options:
  - Server settings (host, port, gRPC, HTTP)
  - Redis/Memcache configuration
  - Stats and metrics
  - Tracing, TLS/mTLS
  - Rate limit behavior
  - Hot key detection parameters
  - Pool on-empty behavior

#### `/src/provider/` - Configuration Providers
- **file_provider.go**: File-based config with hot reload
- **xds_grpc_sotw_provider.go**: xDS management server integration

#### `/src/server/` - Server Implementations
- gRPC server
- HTTP health check server
- Admin interface

#### `/src/stats/` - Statistics Management
- Prometheus exporter
- StatsD/DogStatsD support
- Custom metrics

#### `/src/memcached/` - Memcache Backend
- Alternative to Redis
- Similar interface

#### `/src/trace/` - OpenTelemetry Tracing
- HTTP and gRPC exporters
- Span creation and propagation

### Supporting Components

- `/src/assert/`: Assertion utilities
- `/src/client_cmd/`: gRPC client CLI tool
- `/src/config_check_cmd/`: Configuration validator
- `/src/godogstats/`: DogStatsD integration
- `/src/metrics/`: Metrics collection
- `/src/srv/`: Service utilities
- `/src/utils/`: Common utilities

## Configuration System

### Loading Methods

#### 1. FILE (Default)
- Path: `RUNTIME_ROOT/RUNTIME_SUBDIRECTORY/RUNTIME_APPDIRECTORY/*.yaml`
- Hot reload via symlink swap or file watch
- Multi-domain merging support

#### 2. GRPC_XDS_SOTW
- Protocol: Aggregated Discovery Service (ADS)
- Resource Type: `type.googleapis.com/ratelimit.config.ratelimit.v3.RateLimitConfig`
- TLS support with retry backoff

### Configuration Features

- Domain-based isolation
- Nested descriptor matching
- Wildcard value matching (value*)
- Rate limit units: SECOND, MINUTE, HOUR, DAY, WEEK, MONTH, YEAR
- Unlimited descriptors
- Shadow mode (per-descriptor or global)
- Replaces mechanism for limit override
- Detailed metrics and value-to-metric
- Share threshold for wildcard limits

## Testing Structure

```
test/
├── common/              # Shared test utilities
├── config/              # Config parsing tests
├── integration/         # Integration tests
│   ├── runtime/        # Runtime test configs
│   └── conf/           # Test configurations
├── limiter/            # Limiter unit tests
├── memcached/          # Memcache tests
├── metrics/            # Metrics tests
├── mocks/              # Generated mocks
├── provider/           # Provider tests
├── redis/              # Redis tests + benchmarks
│   └── bench_test.go  # Performance benchmarks
├── server/             # Server tests
├── service/            # Service tests
├── stats/              # Stats tests
└── utils/              # Utility tests
```

### Test Commands

```bash
make tests_unit        # Unit tests only
make tests             # Integration tests
make tests_with_redis  # Tests with Redis instances
make integration_tests # Docker integration tests
```

### Benchmarks

Located in `test/redis/bench_test.go`:
- BenchmarkParallelDoLimit: Maximum throughput
- BenchmarkConstantRateDoLimit: Fixed rate with CPU monitoring

## Entry Points

### 1. Main Service
**File**: `src/service_cmd/main.go`
- Starts the rate limit service
- Binary: `bin/ratelimit`

### 2. Client Tool
**File**: `src/client_cmd/main.go`
- CLI client for testing
- Binary: `bin/client`

### 3. Config Checker
**File**: `src/config_check_cmd/main.go`
- Validates configuration files
- Binary: `bin/config_check`

## Critical Files Reference

### Configuration
- `src/settings/settings.go` - All environment variables
- `api/ratelimit/config/ratelimit/v3/rls_conf.proto` - Proto definitions

### Core Logic
- `src/redis/fixed_cache_impl.go` - Main rate limiting logic
- `src/service/ratelimit.go` - Service implementation

### Build & Deploy
- `Makefile` - Build commands
- `Dockerfile` - Production image (distroless)
- `docker-compose.yml` - Local development
- `docker-compose-cluster.yml` - Redis cluster setup

### Documentation
- `README.md` (1,468 lines) - Main documentation
- `CONTRIBUTING.md` - Contribution guidelines
- `HOTKEY.md` - Hot key feature documentation
- `REDIS-CLUSTER-SETUP.md` - Redis cluster setup
- `docs/hotkey-analysis.md` - Hot key analysis
- `docs/stop-cache-key-increment-guide.md` - Optimization guide

## Recent Major Features

### 1. Hot Key Detection (commit 9b0ef43)
- **Algorithm**: Count-Min Sketch (10MB, depth=4)
- **Threshold**: Configurable (default: 100)
- **Batching**: 300μs flush window
- **Impact**: 75% Redis write reduction, 55% CPU reduction
- **Files**: hotkey_detector.go, hotkey_batcher.go, countmin_sketch.go
- **Config**: 7 new environment variables

### 2. Response Dynamic Metadata (commit 167d0f8)
- Support for returning metadata in responses
- 90+ lines added to service layer

### 3. Redis Sentinel TLS/Auth Fix (commit f52a616)
- Separate auth for Sentinel vs master/replica

### 4. Pool On-Empty Behavior (commit fc44670)
- Configurable: CREATE/ERROR/WAIT
- Prevents connection storms
- Env vars: REDIS_POOL_ON_EMPTY_BEHAVIOR, REDIS_POOL_ON_EMPTY_WAIT_DURATION

### 5. Wildcard Stats Fix (commit e4ac90e)
- Fixed wildcard stats key behavior

### 6. Share Threshold (commit afec97a)
- Share thresholds for wildcard rate limiting

### 7. Value to Metric (commit 6b4f389)
- Include descriptor values in metrics

### 8. Distroless Migration (commit 99d8551)
- Enhanced security posture

## Advanced Features

### Hot Key Detection
- **Memory**: 10MB Count-Min Sketch
- **Threshold**: Configurable (default: 100 accesses)
- **Batching**: 300μs flush window
- **LRU**: Maximum 10,000 tracked keys
- **Decay**: Periodic counter decay (default: 10s)

### Stop Cache Key Increment
- **Purpose**: DDoS protection
- **Mechanism**: GET before INCRBY
- **Env Var**: STOP_CACHE_KEY_INCREMENT_WHEN_OVERLIMIT

### Local Cache
- **Implementation**: freecache (in-memory)
- **Purpose**: Cache over-limit keys
- **Config**: LOCAL_CACHE_SIZE_IN_BYTES

### Monitoring Stack
- **Grafana**: Pre-configured dashboards
- **Prometheus**: Metrics collection
- **Redis Exporter**: Per-node metrics
- **Location**: `monitoring/`

## Deployment

### Docker Images
- **Production**: Distroless-based
- **Integration**: Full Go toolchain
- **Registry**: envoyproxy/ratelimit
- **Tags**: Git commit SHA

### Build Targets
```bash
make compile           # Build binaries
make tests             # Run tests
make docker_image      # Build Docker image
make integration_tests # Run integration tests
```

### Health Checks
- Redis connection health
- Configuration load status
- SIGTERM handling
- Configurable: HEALTHY_WITH_AT_LEAST_ONE_CONFIG_LOADED

### Observability
- **Metrics**: StatsD, DogStatsD, Prometheus, stdout
- **Tracing**: OpenTelemetry (HTTP/gRPC exporters)
- **Logging**: JSON or text format
- **Custom Headers**: Rate limit info in responses

## Current Development Status

**Active Branch**: async-pipeline

**Modified Files** (uncommitted):
- src/redis/driver_impl.go
- src/settings/settings.go
- test/common/common.go
- test/integration/integration_test.go
- test/integration/runtime/current/ratelimit/config/another.yaml
- test/redis/bench_test.go

**New Files** (untracked):
- REDIS-CLUSTER-SETUP.md
- docker-compose-cluster.yml
- docs/ directory
- monitoring/ directory
- scripts/ directory

**Focus Areas**:
1. Redis cluster support
2. Performance benchmarking
3. Comprehensive documentation
4. Monitoring infrastructure

## Architecture Diagram

```
┌─────────────┐
│   Client    │
│ (Envoy/gRPC)│
└──────┬──────┘
       │
       v
┌──────────────────────────────────────┐
│      Ratelimit Service               │
│  ┌────────────────────────────────┐  │
│  │  ShouldRateLimit Handler       │  │
│  │  - Parse descriptors           │  │
│  │  - Check shadow mode           │  │
│  │  - Add custom headers          │  │
│  └────────┬───────────────────────┘  │
│           v                           │
│  ┌────────────────────────────────┐  │
│  │  Limiter                       │  │
│  │  - Generate cache keys         │  │
│  │  - Check local cache           │  │
│  │  - Calculate limits            │  │
│  └────────┬───────────────────────┘  │
└───────────┼───────────────────────────┘
            v
    ┌───────────────┐
    │  Hot Key      │
    │  Detector     │
    │  (Count-Min)  │
    └───────┬───────┘
            v
    ┌───────────────┐        ┌──────────────┐
    │  Batcher      │        │ Local Cache  │
    │  (300μs)      │        │ (freecache)  │
    └───────┬───────┘        └──────────────┘
            v
    ┌───────────────────────┐
    │  Redis Backend        │
    │  - Cluster/Sentinel   │
    │  - Pipeline support   │
    │  - TLS/mTLS           │
    │  - Per-second pools   │
    └───────────────────────┘
```

## Code Metrics

- **Total Lines**: ~6,808 lines of Go code
- **Main Components**: 15+ packages
- **Test Coverage**: Extensive (unit + integration)
- **Dependencies**: 50+ direct dependencies
- **Supported Architectures**: linux/amd64, linux/arm64/v8

## Development Workflow

1. **Make changes**: Edit source code
2. **Run tests**: `make tests`
3. **Build**: `make compile`
4. **Run locally**: `./bin/ratelimit`
5. **Docker build**: `make docker_image`
6. **Integration tests**: `make integration_tests`

## Performance Characteristics

### Hot Key Performance
- **Write Reduction**: 75% for hot keys
- **CPU Reduction**: 55% under load
- **Batching Window**: 300μs
- **Memory Overhead**: 10MB for sketch

### Throughput
- Benchmark results in `test/redis/bench_test.go`
- Scales with Redis cluster size
- Pipeline support for high throughput

## Security Features

1. **Distroless Containers**: Minimal attack surface
2. **TLS/mTLS**: Encrypted Redis connections
3. **Auth Support**: Redis authentication
4. **No Root**: Runs as nonroot user
5. **Dependency Scanning**: CodeQL enabled

## Environment Variables

Over 100 configuration options available via environment variables. See `src/settings/settings.go` for complete list.

### Key Categories:
- Server configuration (ports, TLS)
- Redis/Memcache settings
- Rate limiting behavior
- Stats and metrics
- Tracing
- Hot key detection
- Pool management

## References

- Main Repository: https://github.com/envoyproxy/ratelimit
- Envoy Docs: https://www.envoyproxy.io/docs/envoy/latest/api-v3/service/ratelimit/v3/rls.proto
- Configuration Guide: See README.md
- Hot Key Guide: See HOTKEY.md

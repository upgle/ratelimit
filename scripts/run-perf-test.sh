#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Default configuration
RATELIMIT_PORT=${RATELIMIT_PORT:-8081}
REDIS_URL=${REDIS_URL:-localhost:6379}
CONCURRENCY=${CONCURRENCY:-100}
DURATION=${DURATION:-10s}
CONNECTIONS=${CONNECTIONS:-10}
WARMUP=${WARMUP:-2s}
SCENARIO=${SCENARIO:-all}
LOG_LEVEL=${LOG_LEVEL:-error}
PROMETHEUS_URL=${PROMETHEUS_URL:-http://localhost:9090}

# Directories
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
PERF_DIR="$PROJECT_ROOT/test/perf"
CONFIG_DIR="$PERF_DIR/config"
BIN_DIR="$PROJECT_ROOT/bin"
RESULTS_DIR="$PERF_DIR/results"

# PID file for cleanup
RATELIMIT_PID=""
ENDPOINTS_CONFIG="$PERF_DIR/endpoints.yaml"
INFRA_STARTED=false

print_header() {
    echo ""
    echo -e "${BLUE}================================================================================${NC}"
    echo -e "${BLUE}  $1${NC}"
    echo -e "${BLUE}================================================================================${NC}"
    echo ""
}

print_subheader() {
    echo ""
    echo -e "${CYAN}--------------------------------------------------------------------------------${NC}"
    echo -e "${CYAN}  $1${NC}"
    echo -e "${CYAN}--------------------------------------------------------------------------------${NC}"
    echo ""
}

print_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

cleanup() {
    print_info "Cleaning up..."
    if [ -n "$RATELIMIT_PID" ] && kill -0 "$RATELIMIT_PID" 2>/dev/null; then
        print_info "Stopping ratelimit server (PID: $RATELIMIT_PID)"
        kill "$RATELIMIT_PID" 2>/dev/null || true
        wait "$RATELIMIT_PID" 2>/dev/null || true
    fi

    # Stop infrastructure if we started it
    if [ "$INFRA_STARTED" = true ] && [ "$KEEP_INFRA" != true ]; then
        stop_monitoring_infrastructure
    fi
}

trap cleanup EXIT

# =============================================================================
# Infrastructure Management
# =============================================================================

check_prometheus() {
    if curl -s --connect-timeout 2 "${PROMETHEUS_URL}/api/v1/status/runtimeinfo" > /dev/null 2>&1; then
        return 0
    fi
    return 1
}

start_monitoring_infrastructure() {
    if check_prometheus; then
        print_info "Prometheus is already running at ${PROMETHEUS_URL}"
        return 0
    fi

    print_info "Starting monitoring infrastructure..."
    cd "$PROJECT_ROOT"

    docker compose -f docker-compose-perf.yml up -d grafana prometheus cadvisor redis-exporter memcached-exporter \
        redis-cluster-exporter-1 redis-cluster-exporter-2 redis-cluster-exporter-3 2>/dev/null || \
    docker-compose -f docker-compose-perf.yml up -d grafana prometheus cadvisor redis-exporter memcached-exporter \
        redis-cluster-exporter-1 redis-cluster-exporter-2 redis-cluster-exporter-3 2>/dev/null

    INFRA_STARTED=true

    # Wait for Prometheus to be ready
    local max_attempts=30
    local attempt=0
    print_info "Waiting for Prometheus to be ready..."
    while [ $attempt -lt $max_attempts ]; do
        if check_prometheus; then
            print_info "Prometheus is ready"
            return 0
        fi
        attempt=$((attempt + 1))
        sleep 1
    done

    print_warn "Prometheus not ready after timeout, metrics collection may fail"
    return 1
}

stop_monitoring_infrastructure() {
    print_info "Stopping monitoring infrastructure..."
    cd "$PROJECT_ROOT"

    docker compose -f docker-compose-perf.yml down 2>/dev/null || \
    docker-compose -f docker-compose-perf.yml down 2>/dev/null
}

start_backends() {
    print_info "Starting backend services (Redis, Memcached, Redis Cluster)..."
    cd "$PROJECT_ROOT"

    docker compose -f docker-compose-perf.yml up -d redis memcached \
        redis-master-1 redis-master-2 redis-master-3 redis-cluster-init 2>/dev/null || \
    docker-compose -f docker-compose-perf.yml up -d redis memcached \
        redis-master-1 redis-master-2 redis-master-3 redis-cluster-init 2>/dev/null

    # Wait for services to be ready
    print_info "Waiting for backend services to be ready..."
    sleep 5

    # Wait for Redis cluster to be initialized
    local max_attempts=30
    local attempt=0
    while [ $attempt -lt $max_attempts ]; do
        if redis-cli -p 7001 cluster info 2>/dev/null | grep -q "cluster_state:ok"; then
            print_info "Redis cluster is ready"
            break
        fi
        attempt=$((attempt + 1))
        sleep 1
    done
}

# =============================================================================
# Prometheus Metrics Collection
# =============================================================================

query_prometheus() {
    local query="$1"
    local result

    result=$(curl -s --connect-timeout 5 "${PROMETHEUS_URL}/api/v1/query" \
        --data-urlencode "query=${query}" 2>/dev/null)

    echo "$result"
}

query_prometheus_range() {
    local query="$1"
    local start="$2"
    local end="$3"
    local step="${4:-5s}"
    local result

    result=$(curl -s --connect-timeout 5 "${PROMETHEUS_URL}/api/v1/query_range" \
        --data-urlencode "query=${query}" \
        --data-urlencode "start=${start}" \
        --data-urlencode "end=${end}" \
        --data-urlencode "step=${step}" 2>/dev/null)

    echo "$result"
}

# Get container CPU usage in cores
get_container_cpu() {
    local container_name="$1"
    local start_time="$2"
    local end_time="$3"

    local query="avg(rate(container_cpu_usage_seconds_total{name=\"${container_name}\"}[30s]))"
    local result

    result=$(query_prometheus_range "$query" "$start_time" "$end_time" "5s")

    if echo "$result" | python3 -c "import json,sys; d=json.load(sys.stdin); exit(0 if d.get('status')=='success' and d.get('data',{}).get('result') else 1)" 2>/dev/null; then
        echo "$result" | python3 -c "
import json, sys
data = json.load(sys.stdin)
results = data.get('data', {}).get('result', [])
if results:
    values = [float(v[1]) for v in results[0].get('values', []) if v[1] != 'NaN']
    if values:
        print(f'{sum(values)/len(values):.2f}')
    else:
        print('N/A')
else:
    print('N/A')
"
    else
        echo "N/A"
    fi
}

# Get container memory usage in MB
get_container_memory() {
    local container_name="$1"
    local start_time="$2"
    local end_time="$3"

    local query="avg(container_memory_usage_bytes{name=\"${container_name}\"}) / 1024 / 1024"
    local result

    result=$(query_prometheus_range "$query" "$start_time" "$end_time" "5s")

    if echo "$result" | python3 -c "import json,sys; d=json.load(sys.stdin); exit(0 if d.get('status')=='success' and d.get('data',{}).get('result') else 1)" 2>/dev/null; then
        echo "$result" | python3 -c "
import json, sys
data = json.load(sys.stdin)
results = data.get('data', {}).get('result', [])
if results:
    values = [float(v[1]) for v in results[0].get('values', []) if v[1] != 'NaN']
    if values:
        print(f'{sum(values)/len(values):.2f}')
    else:
        print('N/A')
else:
    print('N/A')
"
    else
        echo "N/A"
    fi
}

# Get Redis metrics
get_redis_metrics() {
    local exporter_target="$1"  # e.g., redis-exporter:9121
    local start_time="$2"
    local end_time="$3"

    # Commands per second
    local cmd_query="rate(redis_commands_processed_total{instance=~\".*${exporter_target}.*\"}[30s])"
    local cmd_result
    cmd_result=$(query_prometheus_range "$cmd_query" "$start_time" "$end_time" "5s")

    local commands_per_sec="N/A"
    if echo "$cmd_result" | python3 -c "import json,sys; d=json.load(sys.stdin); exit(0 if d.get('status')=='success' and d.get('data',{}).get('result') else 1)" 2>/dev/null; then
        commands_per_sec=$(echo "$cmd_result" | python3 -c "
import json, sys
data = json.load(sys.stdin)
results = data.get('data', {}).get('result', [])
if results:
    values = [float(v[1]) for v in results[0].get('values', []) if v[1] != 'NaN']
    if values:
        print(f'{sum(values)/len(values):.0f}')
    else:
        print('N/A')
else:
    print('N/A')
")
    fi

    # Memory usage
    local mem_query="redis_memory_used_bytes{instance=~\".*${exporter_target}.*\"}"
    local mem_result
    mem_result=$(query_prometheus "$mem_query")

    local memory_mb="N/A"
    if echo "$mem_result" | python3 -c "import json,sys; d=json.load(sys.stdin); exit(0 if d.get('status')=='success' and d.get('data',{}).get('result') else 1)" 2>/dev/null; then
        memory_mb=$(echo "$mem_result" | python3 -c "
import json, sys
data = json.load(sys.stdin)
results = data.get('data', {}).get('result', [])
if results:
    value = float(results[0].get('value', [0, 0])[1])
    print(f'{value / 1024 / 1024:.2f}')
else:
    print('N/A')
")
    fi

    echo "{\"commands_per_sec\": \"${commands_per_sec}\", \"memory_mb\": \"${memory_mb}\"}"
}

# Get Memcached metrics
get_memcached_metrics() {
    local start_time="$1"
    local end_time="$2"

    # Commands per second (gets + sets)
    local cmd_query="sum(rate(memcached_commands_total[30s]))"
    local cmd_result
    cmd_result=$(query_prometheus_range "$cmd_query" "$start_time" "$end_time" "5s")

    local commands_per_sec="N/A"
    if echo "$cmd_result" | python3 -c "import json,sys; d=json.load(sys.stdin); exit(0 if d.get('status')=='success' and d.get('data',{}).get('result') else 1)" 2>/dev/null; then
        commands_per_sec=$(echo "$cmd_result" | python3 -c "
import json, sys
data = json.load(sys.stdin)
results = data.get('data', {}).get('result', [])
if results:
    values = [float(v[1]) for v in results[0].get('values', []) if v[1] != 'NaN']
    if values:
        print(f'{sum(values)/len(values):.0f}')
    else:
        print('N/A')
else:
    print('N/A')
")
    fi

    # Memory usage
    local mem_query="memcached_current_bytes"
    local mem_result
    mem_result=$(query_prometheus "$mem_query")

    local memory_mb="N/A"
    if echo "$mem_result" | python3 -c "import json,sys; d=json.load(sys.stdin); exit(0 if d.get('status')=='success' and d.get('data',{}).get('result') else 1)" 2>/dev/null; then
        memory_mb=$(echo "$mem_result" | python3 -c "
import json, sys
data = json.load(sys.stdin)
results = data.get('data', {}).get('result', [])
if results:
    value = float(results[0].get('value', [0, 0])[1])
    print(f'{value / 1024 / 1024:.2f}')
else:
    print('N/A')
")
    fi

    echo "{\"commands_per_sec\": \"${commands_per_sec}\", \"memory_mb\": \"${memory_mb}\"}"
}

collect_metrics() {
    local endpoint_type="$1"
    local start_time="$2"
    local end_time="$3"

    local metrics="{}"

    # Use docker stats for metrics (works better on macOS Docker Desktop)
    case "$endpoint_type" in
        redis)
            local stats=$(docker stats --no-stream --format "{{.CPUPerc}},{{.MemUsage}}" redis-standalone 2>/dev/null || echo "N/A,N/A")
            local cpu=$(echo "$stats" | cut -d',' -f1 | tr -d '%' | xargs)
            local mem_raw=$(echo "$stats" | cut -d',' -f2 | cut -d'/' -f1 | xargs)

            # Convert memory to MB
            local mem="N/A"
            if [ "$mem_raw" != "N/A" ]; then
                mem=$(python3 -c "
s = '$mem_raw'
if 'GiB' in s:
    print(f'{float(s.replace(\"GiB\", \"\")) * 1024:.2f}')
elif 'MiB' in s:
    print(f'{float(s.replace(\"MiB\", \"\")):.2f}')
elif 'KiB' in s:
    print(f'{float(s.replace(\"KiB\", \"\")) / 1024:.2f}')
else:
    print('N/A')
" 2>/dev/null || echo "N/A")
            fi

            # Convert CPU% to cores (100% = 1 core)
            local cpu_cores="N/A"
            if [ "$cpu" != "N/A" ] && [ -n "$cpu" ]; then
                cpu_cores=$(python3 -c "print(f'{float($cpu) / 100:.2f}')" 2>/dev/null || echo "N/A")
            fi

            metrics=$(python3 -c "
import json
print(json.dumps({
    'backend': 'redis-standalone',
    'container_cpu_cores': '$cpu_cores',
    'container_memory_mb': '$mem'
}))
")
            ;;
        cluster)
            # Get aggregate metrics for all cluster nodes
            local total_cpu=0
            local total_mem=0
            local count=0

            for node in 1 2 3; do
                local stats=$(docker stats --no-stream --format "{{.CPUPerc}},{{.MemUsage}}" redis-master-${node} 2>/dev/null || echo "N/A,N/A")
                local cpu=$(echo "$stats" | cut -d',' -f1 | tr -d '%' | xargs)
                local mem_raw=$(echo "$stats" | cut -d',' -f2 | cut -d'/' -f1 | xargs)

                if [ "$cpu" != "N/A" ] && [ -n "$cpu" ]; then
                    local cpu_cores=$(python3 -c "print(float($cpu) / 100)" 2>/dev/null || echo "0")
                    total_cpu=$(python3 -c "print($total_cpu + $cpu_cores)")

                    local mem=$(python3 -c "
s = '$mem_raw'
if 'GiB' in s:
    print(float(s.replace('GiB', '')) * 1024)
elif 'MiB' in s:
    print(float(s.replace('MiB', '')))
elif 'KiB' in s:
    print(float(s.replace('KiB', '')) / 1024)
else:
    print(0)
" 2>/dev/null || echo "0")
                    total_mem=$(python3 -c "print($total_mem + $mem)")
                    count=$((count + 1))
                fi
            done

            local avg_cpu="N/A"
            local total_mem_str="N/A"
            if [ $count -gt 0 ]; then
                avg_cpu=$(python3 -c "print(f'{$total_cpu / $count:.2f}')")
                total_mem_str=$(python3 -c "print(f'{$total_mem:.2f}')")
            fi

            metrics=$(python3 -c "
import json
print(json.dumps({
    'backend': 'redis-cluster',
    'container_cpu_cores_avg': '$avg_cpu',
    'container_memory_mb_total': '$total_mem_str',
    'cluster_nodes': 3
}))
")
            ;;
        memcached)
            local stats=$(docker stats --no-stream --format "{{.CPUPerc}},{{.MemUsage}}" memcached 2>/dev/null || echo "N/A,N/A")
            local cpu=$(echo "$stats" | cut -d',' -f1 | tr -d '%' | xargs)
            local mem_raw=$(echo "$stats" | cut -d',' -f2 | cut -d'/' -f1 | xargs)

            # Convert memory to MB
            local mem="N/A"
            if [ "$mem_raw" != "N/A" ]; then
                mem=$(python3 -c "
s = '$mem_raw'
if 'GiB' in s:
    print(f'{float(s.replace(\"GiB\", \"\")) * 1024:.2f}')
elif 'MiB' in s:
    print(f'{float(s.replace(\"MiB\", \"\")):.2f}')
elif 'KiB' in s:
    print(f'{float(s.replace(\"KiB\", \"\")) / 1024:.2f}')
else:
    print('N/A')
" 2>/dev/null || echo "N/A")
            fi

            # Convert CPU% to cores
            local cpu_cores="N/A"
            if [ "$cpu" != "N/A" ] && [ -n "$cpu" ]; then
                cpu_cores=$(python3 -c "print(f'{float($cpu) / 100:.2f}')" 2>/dev/null || echo "N/A")
            fi

            metrics=$(python3 -c "
import json
print(json.dumps({
    'backend': 'memcached',
    'container_cpu_cores': '$cpu_cores',
    'container_memory_mb': '$mem'
}))
")
            ;;
    esac

    echo "$metrics"
}

# =============================================================================
# Backend Checks
# =============================================================================

check_redis() {
    local redis_url="$1"
    print_info "Checking Redis connection at $redis_url..."

    # Parse Redis URL
    local redis_host=$(echo "$redis_url" | cut -d: -f1)
    local redis_port=$(echo "$redis_url" | cut -d: -f2)

    if nc -z "$redis_host" "$redis_port" 2>/dev/null; then
        print_info "Redis is available at $redis_url"
        return 0
    fi

    print_error "Redis is not available at $redis_url"
    return 1
}

check_memcached() {
    local memcached_url="$1"
    print_info "Checking Memcached connection at $memcached_url..."

    local host=$(echo "$memcached_url" | cut -d: -f1)
    local port=$(echo "$memcached_url" | cut -d: -f2)

    if nc -z "$host" "$port" 2>/dev/null; then
        print_info "Memcached is available at $memcached_url"
        return 0
    fi

    print_warn "Memcached is not available at $memcached_url"
    return 1
}

check_redis_cluster() {
    local first_node="$1"
    print_info "Checking Redis Cluster at $first_node..."

    local host=$(echo "$first_node" | cut -d: -f1)
    local port=$(echo "$first_node" | cut -d: -f2)

    # Try local redis-cli first
    if redis-cli -h "$host" -p "$port" cluster info 2>/dev/null | grep -q "cluster_state:ok"; then
        print_info "Redis Cluster is available and healthy"
        return 0
    fi

    # Fall back to docker exec if local redis-cli not available
    if docker exec redis-master-1 redis-cli -p 7001 cluster info 2>/dev/null | grep -q "cluster_state:ok"; then
        print_info "Redis Cluster is available and healthy (via docker)"
        return 0
    fi

    # Try nc as last resort
    if nc -z "$host" "$port" 2>/dev/null; then
        print_info "Redis Cluster port is available (cluster status unknown)"
        return 0
    fi

    print_warn "Redis Cluster is not available or not healthy at $first_node"
    return 1
}

# =============================================================================
# Build and Server Management
# =============================================================================

build_binaries() {
    print_info "Building ratelimit server..."
    cd "$PROJECT_ROOT"
    make compile

    print_info "Building performance test client..."
    go build -o "$BIN_DIR/perf_test" ./test/perf/perf_client.go
}

stop_ratelimit_server() {
    if [ -n "$RATELIMIT_PID" ] && kill -0 "$RATELIMIT_PID" 2>/dev/null; then
        print_info "Stopping ratelimit server (PID: $RATELIMIT_PID)"
        kill "$RATELIMIT_PID" 2>/dev/null || true
        wait "$RATELIMIT_PID" 2>/dev/null || true
        RATELIMIT_PID=""
        sleep 1
    fi
}

start_ratelimit_server() {
    local endpoint_name="$1"
    shift
    local env_vars=("$@")

    stop_ratelimit_server

    print_info "Starting ratelimit server for endpoint: $endpoint_name"

    # Create runtime directory structure
    mkdir -p "$CONFIG_DIR"

    # Set base environment variables
    export USE_STATSD=false
    export LOG_LEVEL="$LOG_LEVEL"
    export RUNTIME_ROOT="$PERF_DIR"
    export RUNTIME_SUBDIRECTORY=config
    export RUNTIME_IGNOREDOTFILES=true
    export PORT=8080
    export GRPC_PORT="$RATELIMIT_PORT"

    # Apply endpoint-specific environment variables
    for env_var in "${env_vars[@]}"; do
        export "$env_var"
    done

    # Start server in background
    local LOG_FILE="/tmp/ratelimit-perf-${endpoint_name}.log"
    "$BIN_DIR/ratelimit" > "$LOG_FILE" 2>&1 &
    RATELIMIT_PID=$!

    print_info "Ratelimit server started with PID: $RATELIMIT_PID"

    # Wait for server to be ready
    local max_attempts=30
    local attempt=0

    while [ $attempt -lt $max_attempts ]; do
        if nc -z localhost "$RATELIMIT_PORT" 2>/dev/null; then
            print_info "Server is ready on port $RATELIMIT_PORT"
            return 0
        fi
        attempt=$((attempt + 1))
        sleep 0.5
    done

    print_error "Server failed to start within timeout"
    print_error "Check log file: $LOG_FILE"
    return 1
}

# =============================================================================
# Performance Test Execution
# =============================================================================

run_performance_test() {
    local endpoint_name="$1"
    local json_output="$2"
    local settings_str="$3"

    print_info "Running performance test for endpoint: $endpoint_name"

    "$BIN_DIR/perf_test" \
        -addr "localhost:$RATELIMIT_PORT" \
        -c "$CONCURRENCY" \
        -conn "$CONNECTIONS" \
        -d "$DURATION" \
        -warmup "$WARMUP" \
        -scenario "$SCENARIO" \
        -endpoint "$endpoint_name" \
        -json "$json_output" \
        -settings "$settings_str"
}

# Parse YAML configuration using Python (most reliable cross-platform)
parse_endpoints_config() {
    local config_file="$1"

    if [ ! -f "$config_file" ]; then
        print_error "Configuration file not found: $config_file"
        return 1
    fi

    # Use Python to parse YAML (available on most systems)
    python3 << EOF
import yaml
import json
import sys

try:
    with open('$config_file', 'r') as f:
        config = yaml.safe_load(f)

    # Output as JSON for easier parsing in bash
    print(json.dumps(config))
except Exception as e:
    print(f"Error parsing YAML: {e}", file=sys.stderr)
    sys.exit(1)
EOF
}

# =============================================================================
# Multi-Endpoint Test Runner
# =============================================================================

run_multi_endpoint_tests() {
    local config_file="$1"

    print_header "Multi-Endpoint Performance Test"

    # Start monitoring infrastructure if not running
    if [ "$COLLECT_METRICS" = true ]; then
        start_monitoring_infrastructure
    fi

    # Parse configuration
    local config_json
    config_json=$(parse_endpoints_config "$config_file") || return 1

    # Create results directory
    mkdir -p "$RESULTS_DIR"
    local timestamp=$(date +%Y%m%d_%H%M%S)
    local results_file="$RESULTS_DIR/results_${timestamp}.json"

    # Initialize results array
    echo "[]" > "$results_file"

    # Extract test settings from config
    local test_concurrency=$(echo "$config_json" | python3 -c "import json,sys; c=json.load(sys.stdin); print(c.get('test_settings',{}).get('concurrency', $CONCURRENCY))")
    local test_duration=$(echo "$config_json" | python3 -c "import json,sys; c=json.load(sys.stdin); print(c.get('test_settings',{}).get('duration', '$DURATION'))")
    local test_warmup=$(echo "$config_json" | python3 -c "import json,sys; c=json.load(sys.stdin); print(c.get('test_settings',{}).get('warmup', '$WARMUP'))")
    local test_connections=$(echo "$config_json" | python3 -c "import json,sys; c=json.load(sys.stdin); print(c.get('test_settings',{}).get('connections', $CONNECTIONS))")

    # Override with config values
    CONCURRENCY=$test_concurrency
    DURATION=$test_duration
    WARMUP=$test_warmup
    CONNECTIONS=$test_connections

    print_info "Test Settings:"
    echo "  Concurrency:  $CONCURRENCY"
    echo "  Duration:     $DURATION"
    echo "  Warmup:       $WARMUP"
    echo "  Connections:  $CONNECTIONS"
    echo "  Scenario:     $SCENARIO"
    echo "  Metrics:      $COLLECT_METRICS"

    # Get list of endpoints
    local endpoints
    endpoints=$(echo "$config_json" | python3 -c "
import json
import sys

config = json.load(sys.stdin)
endpoints = config.get('endpoints', [])

for ep in endpoints:
    name = ep.get('name', 'unnamed')
    ep_type = ep.get('type', 'redis')
    settings = ep.get('settings', {})

    # Convert settings to KEY=VALUE format
    settings_str = '|'.join([f'{k}={v}' for k, v in settings.items()])

    print(f'{name}:{ep_type}:{settings_str}')
")

    if [ -z "$endpoints" ]; then
        print_error "No endpoints found in configuration"
        return 1
    fi

    local endpoint_count=0
    local successful_count=0
    local all_results="[]"

    # Process each endpoint
    while IFS= read -r endpoint_line; do
        [ -z "$endpoint_line" ] && continue

        endpoint_count=$((endpoint_count + 1))

        local name=$(echo "$endpoint_line" | cut -d: -f1)
        local ep_type=$(echo "$endpoint_line" | cut -d: -f2)
        local settings_str=$(echo "$endpoint_line" | cut -d: -f3-)

        print_subheader "Endpoint $endpoint_count: $name ($ep_type)"

        # Convert settings string to array
        local env_vars=()
        if [ -n "$settings_str" ]; then
            IFS='|' read -ra settings_arr <<< "$settings_str"
            for setting in "${settings_arr[@]}"; do
                env_vars+=("$setting")
            done
        fi

        # Determine backend type for metrics
        local backend_type="redis"
        case "$ep_type" in
            redis)
                # Check if it's a cluster by looking at REDIS_TYPE setting
                for setting in "${env_vars[@]}"; do
                    if [[ "$setting" == "REDIS_TYPE=cluster" ]]; then
                        backend_type="cluster"
                        break
                    fi
                done
                ;;
            memcached)
                backend_type="memcached"
                ;;
        esac

        # Check backend availability
        local backend_available=true
        case "$ep_type" in
            redis)
                local redis_url=""
                for setting in "${env_vars[@]}"; do
                    if [[ "$setting" == REDIS_URL=* ]]; then
                        redis_url="${setting#REDIS_URL=}"
                        break
                    fi
                done
                [ -z "$redis_url" ] && redis_url="localhost:6379"

                # Check if it's a cluster
                if [ "$backend_type" = "cluster" ]; then
                    local first_node=$(echo "$redis_url" | cut -d, -f1)
                    check_redis_cluster "$first_node" || backend_available=false
                else
                    check_redis "$redis_url" || backend_available=false
                fi
                ;;
            memcached)
                local memcached_url=""
                for setting in "${env_vars[@]}"; do
                    if [[ "$setting" == MEMCACHE_HOST_PORT=* ]]; then
                        memcached_url="${setting#MEMCACHE_HOST_PORT=}"
                        break
                    fi
                done
                [ -z "$memcached_url" ] && memcached_url="localhost:11211"
                check_memcached "$memcached_url" || backend_available=false
                ;;
        esac

        if [ "$backend_available" = false ]; then
            print_warn "Skipping endpoint $name - backend not available"
            continue
        fi

        # Start server with endpoint configuration
        if ! start_ratelimit_server "$name" "${env_vars[@]}"; then
            print_error "Failed to start server for endpoint: $name"
            continue
        fi

        sleep 1

        # Build settings string for JSON output (comma-separated KEY=VALUE)
        local settings_csv=""
        if [ -n "$settings_str" ]; then
            # Convert pipe-separated to comma-separated
            settings_csv=$(echo "$settings_str" | tr '|' ',')
        fi

        # Record test start time for metrics
        local test_start_time=$(date +%s)

        # Run performance test
        local temp_json="/tmp/perf_${name}_${timestamp}.json"
        if run_performance_test "$name" "$temp_json" "$settings_csv"; then
            successful_count=$((successful_count + 1))

            # Record test end time
            local test_end_time=$(date +%s)

            # Collect metrics if enabled
            local metrics="{}"
            if [ "$COLLECT_METRICS" = true ]; then
                print_info "Collecting resource metrics..."
                metrics=$(collect_metrics "$backend_type" "$test_start_time" "$test_end_time")
            fi

            # Merge results with metrics
            if [ -f "$temp_json" ]; then
                all_results=$(python3 -c "
import json
existing = json.loads('$all_results')
with open('$temp_json') as f:
    new = json.load(f)

metrics = json.loads('$metrics')

# Add metrics to each result
for result in new:
    result['resource_metrics'] = metrics

existing.extend(new)
print(json.dumps(existing))
")
                rm -f "$temp_json"
            fi
        else
            print_error "Performance test failed for endpoint: $name"
        fi

        # Stop server before next endpoint
        stop_ratelimit_server
    done <<< "$endpoints"

    # Save final results
    echo "$all_results" | python3 -c "import json,sys; print(json.dumps(json.load(sys.stdin), indent=2))" > "$results_file"

    # Print summary
    print_header "Test Summary"
    echo "  Endpoints tested: $successful_count / $endpoint_count"
    echo "  Results saved to: $results_file"
    echo ""

    # Print comparison table
    print_comparison_from_json "$results_file"
}

print_comparison_from_json() {
    local json_file="$1"

    python3 << EOF
import json

with open('$json_file') as f:
    results = json.load(f)

if not results:
    print("No results to display")
    exit(0)

# Group by endpoint
endpoints = {}
for r in results:
    ep = r.get('endpoint', 'unknown')
    if ep not in endpoints:
        endpoints[ep] = []
    endpoints[ep].append(r)

# Print header
print("=" * 120)
print("  Performance Comparison")
print("=" * 120)
print()

# Print table header
header = f"{'Endpoint':<25} {'Scenario':<15} {'RPS':>10} {'Avg':>10} {'P50':>10} {'P95':>10} {'P99':>10} {'CPU':>10} {'Mem MB':>10}"
print(header)
print("-" * 125)

# Print rows
for ep_name, ep_results in endpoints.items():
    for r in ep_results:
        scenario = r.get('scenario', 'unknown')
        rps = r.get('rps', 0)
        lat = r.get('latencies', {})
        metrics = r.get('resource_metrics', {})

        avg = f"{lat.get('avg_us', 0) / 1000:.2f}ms"
        p50 = f"{lat.get('p50_us', 0) / 1000:.2f}ms"
        p95 = f"{lat.get('p95_us', 0) / 1000:.2f}ms"
        p99 = f"{lat.get('p99_us', 0) / 1000:.2f}ms"

        # Get CPU (in cores) and memory from metrics
        cpu = metrics.get('container_cpu_cores', metrics.get('container_cpu_cores_avg', 'N/A'))
        if cpu != 'N/A':
            try:
                cpu = f"{float(cpu):.2f}"
            except:
                pass
        mem = metrics.get('container_memory_mb', metrics.get('container_memory_mb_total', 'N/A'))

        ep_display = ep_name[:22] + "..." if len(ep_name) > 25 else ep_name
        print(f"{ep_display:<25} {scenario:<15} {rps:>10.0f} {avg:>10} {p50:>10} {p95:>10} {p99:>10} {str(cpu):>10} {str(mem):>10}")

print()

# Print best performers per scenario
print("=" * 120)
print("  Best Performers (by RPS)")
print("=" * 120)
print()

scenarios = set(r['scenario'] for r in results)
for scenario in sorted(scenarios):
    scenario_results = [r for r in results if r['scenario'] == scenario]
    best = max(scenario_results, key=lambda x: x.get('rps', 0))
    print(f"  {scenario}: {best['endpoint']} ({best['rps']:.0f} RPS)")

print()

# Print resource metrics summary
print("=" * 120)
print("  Resource Metrics Summary")
print("=" * 120)
print()

for ep_name, ep_results in endpoints.items():
    # Get first result's metrics (all scenarios have same metrics for endpoint)
    if ep_results:
        metrics = ep_results[0].get('resource_metrics', {})
        if metrics:
            backend = metrics.get('backend', 'unknown')
            cpu = metrics.get('container_cpu_cores', metrics.get('container_cpu_cores_avg', 'N/A'))
            if cpu != 'N/A':
                try:
                    cpu = f"{float(cpu):.2f}"
                except:
                    pass
            mem = metrics.get('container_memory_mb', metrics.get('container_memory_mb_total', 'N/A'))
            print(f"  {ep_name}: CPU={cpu} cores, Memory={mem}MB ({backend})")

print()
EOF
}

# =============================================================================
# Simple Single Endpoint Test
# =============================================================================

run_simple_test() {
    print_header "Running Performance Test"

    echo "Configuration:"
    echo "  Server:       localhost:$RATELIMIT_PORT"
    echo "  Concurrency:  $CONCURRENCY"
    echo "  Connections:  $CONNECTIONS"
    echo "  Duration:     $DURATION"
    echo "  Warmup:       $WARMUP"
    echo "  Scenario:     $SCENARIO"
    echo ""

    "$BIN_DIR/perf_test" \
        -addr "localhost:$RATELIMIT_PORT" \
        -c "$CONCURRENCY" \
        -conn "$CONNECTIONS" \
        -d "$DURATION" \
        -warmup "$WARMUP" \
        -scenario "$SCENARIO"
}

# =============================================================================
# Usage and Argument Parsing
# =============================================================================

show_usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  -c, --concurrency NUM    Number of concurrent workers (default: $CONCURRENCY)"
    echo "  -d, --duration DURATION  Test duration (default: $DURATION)"
    echo "  -n, --connections NUM    Number of gRPC connections (default: $CONNECTIONS)"
    echo "  -w, --warmup DURATION    Warmup duration (default: $WARMUP)"
    echo "  -s, --scenario SCENARIO  Test scenario: fixed, variable, mixed2, mixed10, all (default: $SCENARIO)"
    echo "  -r, --redis URL          Redis URL (default: $REDIS_URL)"
    echo "  -p, --port PORT          gRPC port (default: $RATELIMIT_PORT)"
    echo "  -l, --log-level LEVEL    Log level: debug, info, warn, error (default: $LOG_LEVEL)"
    echo "  -e, --endpoints FILE     Endpoints configuration file for multi-endpoint testing"
    echo "  -m, --metrics            Enable CPU/memory metrics collection via Prometheus"
    echo "  --prometheus URL         Prometheus URL (default: $PROMETHEUS_URL)"
    echo "  --start-backends         Start backend services (Redis, Memcached, Redis Cluster)"
    echo "  --keep-infra             Keep monitoring infrastructure running after test"
    echo "  --skip-build             Skip building binaries"
    echo "  --skip-server            Don't start server (use existing)"
    echo "  -h, --help               Show this help"
    echo ""
    echo "Examples:"
    echo "  $0                                    # Run simple test with defaults"
    echo "  $0 -c 200 -d 30s -s fixed            # 200 workers, 30s, fixed key only"
    echo "  $0 -e test/perf/endpoints.yaml -m   # Run multi-endpoint tests with metrics"
    echo "  $0 --start-backends -e test/perf/endpoints.yaml -m"
    echo "  $0 --skip-server -s mixed             # Use existing server"
    echo ""
    echo "Endpoint Configuration:"
    echo "  Create a YAML file with endpoint configurations."
    echo "  See test/perf/endpoints.yaml for example."
    echo ""
    echo "Metrics Collection:"
    echo "  Use -m flag to collect CPU/memory metrics from backends."
    echo "  Requires Prometheus to be running (auto-starts if not available)."
    echo ""
}

# Parse command line arguments
SKIP_BUILD=false
SKIP_SERVER=false
MULTI_ENDPOINT=false
COLLECT_METRICS=false
START_BACKENDS=false
KEEP_INFRA=false

while [[ $# -gt 0 ]]; do
    case $1 in
        -c|--concurrency)
            CONCURRENCY="$2"
            shift 2
            ;;
        -d|--duration)
            DURATION="$2"
            shift 2
            ;;
        -n|--connections)
            CONNECTIONS="$2"
            shift 2
            ;;
        -w|--warmup)
            WARMUP="$2"
            shift 2
            ;;
        -s|--scenario)
            SCENARIO="$2"
            shift 2
            ;;
        -r|--redis)
            REDIS_URL="$2"
            shift 2
            ;;
        -p|--port)
            RATELIMIT_PORT="$2"
            shift 2
            ;;
        -l|--log-level)
            LOG_LEVEL="$2"
            shift 2
            ;;
        -e|--endpoints)
            ENDPOINTS_CONFIG="$2"
            MULTI_ENDPOINT=true
            shift 2
            ;;
        -m|--metrics)
            COLLECT_METRICS=true
            shift
            ;;
        --prometheus)
            PROMETHEUS_URL="$2"
            shift 2
            ;;
        --start-backends)
            START_BACKENDS=true
            shift
            ;;
        --keep-infra)
            KEEP_INFRA=true
            shift
            ;;
        --skip-build)
            SKIP_BUILD=true
            shift
            ;;
        --skip-server)
            SKIP_SERVER=true
            shift
            ;;
        -h|--help)
            show_usage
            exit 0
            ;;
        *)
            print_error "Unknown option: $1"
            show_usage
            exit 1
            ;;
    esac
done

# =============================================================================
# Main Execution
# =============================================================================

print_header "Envoy Ratelimit Performance Test"

# Build if needed
if [ "$SKIP_BUILD" = false ]; then
    build_binaries
fi

# Start backends if requested
if [ "$START_BACKENDS" = true ]; then
    start_backends
fi

# Run appropriate test mode
if [ "$MULTI_ENDPOINT" = true ]; then
    # Multi-endpoint test
    run_multi_endpoint_tests "$ENDPOINTS_CONFIG"
else
    # Simple single endpoint test
    if [ "$SKIP_SERVER" = false ]; then
        # Check Redis for simple test
        check_redis "$REDIS_URL" || exit 1

        # Set up default environment
        export REDIS_SOCKET_TYPE=tcp
        export REDIS_TYPE=SINGLE
        export REDIS_URL="$REDIS_URL"

        start_ratelimit_server "default" \
            "REDIS_SOCKET_TYPE=tcp" \
            "REDIS_TYPE=SINGLE" \
            "REDIS_URL=$REDIS_URL"
        sleep 1
    fi

    run_simple_test
fi

print_header "Test Complete"

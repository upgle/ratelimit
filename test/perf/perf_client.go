package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	pb_struct "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type TestScenario int

const (
	FixedKey TestScenario = iota
	VariableKey
	MixedKey2  // 2 keys: 1 fixed + 1 variable
	MixedKey10 // 10 keys: 5 fixed + 5 variable
)

func (s TestScenario) String() string {
	switch s {
	case FixedKey:
		return "fixed_key"
	case VariableKey:
		return "variable_key"
	case MixedKey2:
		return "mixed_2keys"
	case MixedKey10:
		return "mixed_10keys"
	default:
		return "unknown"
	}
}

type LatencyStats struct {
	latencies []time.Duration
	mu        sync.Mutex
}

func (ls *LatencyStats) Add(d time.Duration) {
	ls.mu.Lock()
	ls.latencies = append(ls.latencies, d)
	ls.mu.Unlock()
}

func (ls *LatencyStats) Calculate() map[string]time.Duration {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	if len(ls.latencies) == 0 {
		return nil
	}

	sorted := make([]time.Duration, len(ls.latencies))
	copy(sorted, ls.latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	percentiles := map[string]float64{
		"min":  0,
		"p50":  0.50,
		"p75":  0.75,
		"p90":  0.90,
		"p95":  0.95,
		"p99":  0.99,
		"p999": 0.999,
		"max":  1.0,
	}

	results := make(map[string]time.Duration)
	for name, p := range percentiles {
		idx := int(float64(len(sorted)-1) * p)
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}
		results[name] = sorted[idx]
	}

	// Calculate average
	var total time.Duration
	for _, d := range sorted {
		total += d
	}
	results["avg"] = total / time.Duration(len(sorted))

	return results
}

// JSON output structures
type JSONLatencies struct {
	MinUs  int64 `json:"min_us"`
	AvgUs  int64 `json:"avg_us"`
	P50Us  int64 `json:"p50_us"`
	P75Us  int64 `json:"p75_us"`
	P90Us  int64 `json:"p90_us"`
	P95Us  int64 `json:"p95_us"`
	P99Us  int64 `json:"p99_us"`
	P999Us int64 `json:"p999_us"`
	MaxUs  int64 `json:"max_us"`
}

type JSONTestConfig struct {
	Concurrency int    `json:"concurrency"`
	Connections int    `json:"connections"`
	DurationSec int    `json:"duration_sec"`
	WarmupSec   int    `json:"warmup_sec"`
	ServerAddr  string `json:"server_addr"`
}

type JSONEndpointSettings struct {
	RedisType         string `json:"redis_type,omitempty"`
	RedisURL          string `json:"redis_url,omitempty"`
	RedisPoolSize     string `json:"redis_pool_size,omitempty"`
	HotKeyEnabled     string `json:"hotkey_enabled,omitempty"`
	HotKeyThreshold   string `json:"hotkey_threshold,omitempty"`
	HotKeyFlushWindow string `json:"hotkey_flush_window,omitempty"`
	BackendType       string `json:"backend_type,omitempty"`
	MemcacheHostPort  string `json:"memcache_host_port,omitempty"`
}

type JSONResult struct {
	Endpoint      string               `json:"endpoint"`
	Scenario      string               `json:"scenario"`
	TotalRequests int64                `json:"total_requests"`
	SuccessCount  int64                `json:"success_count"`
	ErrorCount    int64                `json:"error_count"`
	DurationMs    int64                `json:"duration_ms"`
	RPS           float64              `json:"rps"`
	Latencies     JSONLatencies        `json:"latencies"`
	TestConfig    JSONTestConfig       `json:"test_config,omitempty"`
	Settings      JSONEndpointSettings `json:"settings,omitempty"`
}

type BenchmarkResult struct {
	Endpoint      string
	Scenario      TestScenario
	TotalRequests int64
	SuccessCount  int64
	ErrorCount    int64
	Duration      time.Duration
	RPS           float64
	Latencies     map[string]time.Duration
	TestConfig    JSONTestConfig
	Settings      JSONEndpointSettings
}

func (r *BenchmarkResult) ToJSON() JSONResult {
	jr := JSONResult{
		Endpoint:      r.Endpoint,
		Scenario:      r.Scenario.String(),
		TotalRequests: r.TotalRequests,
		SuccessCount:  r.SuccessCount,
		ErrorCount:    r.ErrorCount,
		DurationMs:    r.Duration.Milliseconds(),
		RPS:           r.RPS,
		TestConfig:    r.TestConfig,
		Settings:      r.Settings,
	}

	if r.Latencies != nil {
		jr.Latencies = JSONLatencies{
			MinUs:  r.Latencies["min"].Microseconds(),
			AvgUs:  r.Latencies["avg"].Microseconds(),
			P50Us:  r.Latencies["p50"].Microseconds(),
			P75Us:  r.Latencies["p75"].Microseconds(),
			P90Us:  r.Latencies["p90"].Microseconds(),
			P95Us:  r.Latencies["p95"].Microseconds(),
			P99Us:  r.Latencies["p99"].Microseconds(),
			P999Us: r.Latencies["p999"].Microseconds(),
			MaxUs:  r.Latencies["max"].Microseconds(),
		}
	}

	return jr
}

func runBenchmark(
	addr string,
	endpoint string,
	scenario TestScenario,
	concurrency int,
	duration time.Duration,
	connections int,
	testConfig JSONTestConfig,
	settings JSONEndpointSettings,
) (*BenchmarkResult, error) {
	// Create connection pool
	conns := make([]*grpc.ClientConn, connections)
	clients := make([]pb.RateLimitServiceClient, connections)

	for i := 0; i < connections; i++ {
		conn, err := grpc.Dial(addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
			grpc.WithTimeout(5*time.Second),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to connect: %v", err)
		}
		conns[i] = conn
		clients[i] = pb.NewRateLimitServiceClient(conn)
	}

	defer func() {
		for _, conn := range conns {
			conn.Close()
		}
	}()

	var (
		totalRequests int64
		successCount  int64
		errorCount    int64
		wg            sync.WaitGroup
		stats         = &LatencyStats{}
	)

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	startTime := time.Now()

	// Start workers
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			client := clients[workerID%connections]
			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))

			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				req := buildRequest(scenario, rng)
				reqStart := time.Now()

				_, err := client.ShouldRateLimit(ctx, req)
				latency := time.Since(reqStart)

				atomic.AddInt64(&totalRequests, 1)

				if err != nil {
					if ctx.Err() != nil {
						return // Context cancelled, normal exit
					}
					fmt.Println(err.Error())
					atomic.AddInt64(&errorCount, 1)
				} else {
					atomic.AddInt64(&successCount, 1)
					stats.Add(latency)
				}
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(startTime)

	result := &BenchmarkResult{
		Endpoint:      endpoint,
		Scenario:      scenario,
		TotalRequests: totalRequests,
		SuccessCount:  successCount,
		ErrorCount:    errorCount,
		Duration:      elapsed,
		RPS:           float64(totalRequests) / elapsed.Seconds(),
		Latencies:     stats.Calculate(),
		TestConfig:    testConfig,
		Settings:      settings,
	}

	return result, nil
}

func buildRequest(scenario TestScenario, rng *rand.Rand) *pb.RateLimitRequest {
	switch scenario {
	case FixedKey:
		// Always the same key - tests hot key detection
		return &pb.RateLimitRequest{
			Domain: "perf_test",
			Descriptors: []*pb_struct.RateLimitDescriptor{
				{
					Entries: []*pb_struct.RateLimitDescriptor_Entry{
						{Key: "api_key", Value: "fixed_key"},
					},
				},
			},
			HitsAddend: 1,
		}

	case VariableKey:
		// Different key each time - tests unique key handling
		return &pb.RateLimitRequest{
			Domain: "perf_test",
			Descriptors: []*pb_struct.RateLimitDescriptor{
				{
					Entries: []*pb_struct.RateLimitDescriptor_Entry{
						{Key: "api_key", Value: fmt.Sprintf("key_%d", rng.Int63())},
					},
				},
			},
			HitsAddend: 1,
		}

	case MixedKey2:
		// Mixed scenario: 1 fixed key + 1 variable key (2 total)
		return &pb.RateLimitRequest{
			Domain: "perf_test",
			Descriptors: []*pb_struct.RateLimitDescriptor{
				{
					Entries: []*pb_struct.RateLimitDescriptor_Entry{
						{Key: "nested_fixed_1", Value: "value_1"},
						{Key: "var_1", Value: fmt.Sprintf("v_%d", rng.Int63())},
					},
				},
			},
			HitsAddend: 1,
		}

	case MixedKey10:
		group := rng.Int63()
		// Mixed scenario: 10 separate descriptors (5 fixed keys + 5 variable keys)
		// Each descriptor is processed independently and hits Redis separately
		return &pb.RateLimitRequest{
			Domain: "perf_test",
			Descriptors: []*pb_struct.RateLimitDescriptor{
				// 5 fixed key descriptors - same key each time (hot keys)
				{Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "fixed_1", Value: "value_1"}}},
				{Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "fixed_2", Value: "value_2"}}},
				{Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "fixed_3", Value: "value_3"}}},
				{Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "fixed_4", Value: "value_4"}}},
				{Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "fixed_5", Value: "value_5"}}},
				//// 5 variable key descriptors - different value each time (unique keys)
				{Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "var_1", Value: fmt.Sprintf("v_%d_%d", group, rng.Int63())}}},
				{Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "var_2", Value: fmt.Sprintf("v_%d_%d", group, rng.Int63())}}},
				{Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "var_3", Value: fmt.Sprintf("v_%d_%d", group, rng.Int63())}}},
				{Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "var_4", Value: fmt.Sprintf("v_%d_%d", group, rng.Int63())}}},
				{Entries: []*pb_struct.RateLimitDescriptor_Entry{{Key: "var_5", Value: fmt.Sprintf("v_%d_%d", group, rng.Int63())}}},
			},
			HitsAddend: 1,
		}

	default:
		return nil
	}
}

func printResult(result *BenchmarkResult) {
	fmt.Printf("\n")
	fmt.Printf("================================================================================\n")
	if result.Endpoint != "" {
		fmt.Printf("  Endpoint: %s | Scenario: %s\n", result.Endpoint, result.Scenario)
	} else {
		fmt.Printf("  Scenario: %s\n", result.Scenario)
	}
	fmt.Printf("================================================================================\n")
	fmt.Printf("\n")
	fmt.Printf("  Summary:\n")
	fmt.Printf("    Total Requests:  %d\n", result.TotalRequests)
	fmt.Printf("    Successful:      %d\n", result.SuccessCount)
	fmt.Printf("    Errors:          %d\n", result.ErrorCount)
	fmt.Printf("    Duration:        %v\n", result.Duration.Round(time.Millisecond))
	fmt.Printf("    Requests/sec:    %.2f\n", result.RPS)
	fmt.Printf("\n")

	if result.Latencies != nil {
		fmt.Printf("  Latency Distribution:\n")
		fmt.Printf("    %-8s %12s\n", "Metric", "Value")
		fmt.Printf("    %-8s %12s\n", "------", "-----")

		order := []string{"min", "avg", "p50", "p75", "p90", "p95", "p99", "p999", "max"}
		for _, name := range order {
			if v, ok := result.Latencies[name]; ok {
				fmt.Printf("    %-8s %12v\n", name, v.Round(time.Microsecond))
			}
		}
	}
	fmt.Printf("\n")
}

func printComparisonTable(results []*BenchmarkResult) {
	fmt.Printf("\n")
	fmt.Printf("================================================================================\n")
	fmt.Printf("  Comparison Summary\n")
	fmt.Printf("================================================================================\n")
	fmt.Printf("\n")

	// Check if we have multiple endpoints
	hasMultipleEndpoints := false
	endpoints := make(map[string]bool)
	for _, r := range results {
		if r.Endpoint != "" {
			endpoints[r.Endpoint] = true
		}
	}
	hasMultipleEndpoints = len(endpoints) > 1

	// Header
	if hasMultipleEndpoints {
		fmt.Printf("  %-20s %-15s %10s %10s %10s %10s %10s %10s\n",
			"Endpoint", "Scenario", "RPS", "Avg", "P50", "P95", "P99", "P99.9")
		fmt.Printf("  %-20s %-15s %10s %10s %10s %10s %10s %10s\n",
			"--------------------", "---------------", "----------", "----------", "----------", "----------", "----------", "----------")
	} else {
		fmt.Printf("  %-15s %12s %12s %12s %12s %12s %12s\n",
			"Scenario", "RPS", "Avg", "P50", "P95", "P99", "P99.9")
		fmt.Printf("  %-15s %12s %12s %12s %12s %12s %12s\n",
			"---------------", "------------", "------------", "------------", "------------", "------------", "------------")
	}

	for _, r := range results {
		if r.Latencies != nil {
			if hasMultipleEndpoints {
				endpointName := r.Endpoint
				if len(endpointName) > 20 {
					endpointName = endpointName[:17] + "..."
				}
				fmt.Printf("  %-20s %-15s %10.0f %10v %10v %10v %10v %10v\n",
					endpointName,
					r.Scenario,
					r.RPS,
					r.Latencies["avg"].Round(time.Microsecond),
					r.Latencies["p50"].Round(time.Microsecond),
					r.Latencies["p95"].Round(time.Microsecond),
					r.Latencies["p99"].Round(time.Microsecond),
					r.Latencies["p999"].Round(time.Microsecond),
				)
			} else {
				fmt.Printf("  %-15s %12.0f %12v %12v %12v %12v %12v\n",
					r.Scenario,
					r.RPS,
					r.Latencies["avg"].Round(time.Microsecond),
					r.Latencies["p50"].Round(time.Microsecond),
					r.Latencies["p95"].Round(time.Microsecond),
					r.Latencies["p99"].Round(time.Microsecond),
					r.Latencies["p999"].Round(time.Microsecond),
				)
			}
		}
	}
	fmt.Printf("\n")
}

func writeJSONResults(results []*BenchmarkResult, outputFile string) error {
	jsonResults := make([]JSONResult, len(results))
	for i, r := range results {
		jsonResults[i] = r.ToJSON()
	}

	data, err := json.MarshalIndent(jsonResults, "", "  ")
	if err != nil {
		return err
	}

	if outputFile == "-" {
		fmt.Println(string(data))
		return nil
	}

	return os.WriteFile(outputFile, data, 0644)
}

func parseSettings(settingsStr string) JSONEndpointSettings {
	settings := JSONEndpointSettings{}
	if settingsStr == "" {
		return settings
	}

	pairs := strings.Split(settingsStr, ",")
	for _, pair := range pairs {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key, value := kv[0], kv[1]
		switch key {
		case "REDIS_TYPE":
			settings.RedisType = value
		case "REDIS_URL":
			settings.RedisURL = value
		case "REDIS_POOL_SIZE":
			settings.RedisPoolSize = value
		case "HOT_KEY_DETECTION_ENABLED":
			settings.HotKeyEnabled = value
		case "HOT_KEY_THRESHOLD":
			settings.HotKeyThreshold = value
		case "HOT_KEY_FLUSH_WINDOW":
			settings.HotKeyFlushWindow = value
		case "BACKEND_TYPE":
			settings.BackendType = value
		case "MEMCACHE_HOST_PORT":
			settings.MemcacheHostPort = value
		}
	}
	return settings
}

func main() {
	addr := flag.String("addr", "localhost:8081", "gRPC server address")
	concurrency := flag.Int("c", 100, "Number of concurrent workers")
	duration := flag.Duration("d", 10*time.Second, "Test duration")
	connections := flag.Int("conn", 10, "Number of gRPC connections")
	scenario := flag.String("scenario", "all", "Test scenario: fixed, variable, mixed2, mixed10, or all")
	warmup := flag.Duration("warmup", 2*time.Second, "Warmup duration before test")
	endpoint := flag.String("endpoint", "", "Endpoint name for labeling results")
	jsonOutput := flag.String("json", "", "Output results as JSON to file (use '-' for stdout)")
	quiet := flag.Bool("q", false, "Quiet mode - only output JSON (requires -json)")
	settingsStr := flag.String("settings", "", "Endpoint settings as KEY=VALUE,KEY2=VALUE2 format")
	flag.Parse()

	quietMode := *quiet && *jsonOutput != ""

	// Parse settings
	settings := parseSettings(*settingsStr)

	// Build test config
	testConfig := JSONTestConfig{
		Concurrency: *concurrency,
		Connections: *connections,
		DurationSec: int(duration.Seconds()),
		WarmupSec:   int(warmup.Seconds()),
		ServerAddr:  *addr,
	}

	if !quietMode {
		fmt.Printf("\n")
		fmt.Printf("================================================================================\n")
		fmt.Printf("  gRPC Performance Test - Envoy Ratelimit Service\n")
		fmt.Printf("================================================================================\n")
		fmt.Printf("\n")
		fmt.Printf("  Configuration:\n")
		fmt.Printf("    Server Address:  %s\n", *addr)
		fmt.Printf("    Concurrency:     %d workers\n", *concurrency)
		fmt.Printf("    Connections:     %d\n", *connections)
		fmt.Printf("    Duration:        %v\n", *duration)
		fmt.Printf("    Warmup:          %v\n", *warmup)
		fmt.Printf("    Scenario:        %s\n", *scenario)
		if *endpoint != "" {
			fmt.Printf("    Endpoint:        %s\n", *endpoint)
		}
		fmt.Printf("\n")
	}

	// Warmup
	if *warmup > 0 {
		if !quietMode {
			fmt.Printf("  Running warmup for %v...\n", *warmup)
		}
		_, err := runBenchmark(*addr, *endpoint, FixedKey, *concurrency/2, *warmup, *connections, testConfig, settings)
		if err != nil {
			log.Fatalf("Warmup failed: %v", err)
		}
		if !quietMode {
			fmt.Printf("  Warmup complete.\n")
		}
	}

	var scenarios []TestScenario
	switch *scenario {
	case "fixed":
		scenarios = []TestScenario{FixedKey}
	case "variable":
		scenarios = []TestScenario{VariableKey}
	case "mixed", "mixed2":
		scenarios = []TestScenario{MixedKey2}
	case "mixed10":
		scenarios = []TestScenario{MixedKey10}
	case "all":
		scenarios = []TestScenario{FixedKey, VariableKey, MixedKey2, MixedKey10}
	default:
		fmt.Fprintf(os.Stderr, "Unknown scenario: %s\n", *scenario)
		os.Exit(1)
	}

	var results []*BenchmarkResult

	for _, s := range scenarios {
		if !quietMode {
			fmt.Printf("  Running %s scenario...\n", s)
		}
		result, err := runBenchmark(*addr, *endpoint, s, *concurrency, *duration, *connections, testConfig, settings)
		if err != nil {
			log.Fatalf("Benchmark failed for %s: %v", s, err)
		}
		results = append(results, result)

		if !quietMode {
			printResult(result)
		}

		// Brief pause between scenarios
		if len(scenarios) > 1 {
			time.Sleep(1 * time.Second)
		}
	}

	if !quietMode && len(results) > 1 {
		printComparisonTable(results)
	}

	// Output JSON if requested
	if *jsonOutput != "" {
		if err := writeJSONResults(results, *jsonOutput); err != nil {
			log.Fatalf("Failed to write JSON results: %v", err)
		}
	}
}

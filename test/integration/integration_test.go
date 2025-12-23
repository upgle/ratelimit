//go:build integration

package integration_test

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	"github.com/kelseyhightower/envconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/envoyproxy/ratelimit/src/memcached"
	"github.com/envoyproxy/ratelimit/src/service_cmd/runner"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/utils"
	"github.com/envoyproxy/ratelimit/test/common"
)

var projectDir = os.Getenv("PROJECT_DIR")

func init() {
	os.Setenv("USE_STATSD", "false")
	os.Setenv("HOT_KEY_DETECTION_ENABLED", "true")
	os.Setenv("HOT_KEY_THRESHOLD", "10")
	os.Setenv("HOT_KEY_FLUSH_WINDOW", "50us")

	//os.Setenv("REDIS_POOL_SIZE", "10")
	os.Setenv("REDIS_TYPE", "cluster")

	// Memcache does async increments, which can cause race conditions during
	// testing. Force sync increments so the quotas are predictable during testing.
	memcached.AutoFlushForIntegrationTests = true
}

func defaultSettings() settings.Settings {
	// Fetch the default setting values.
	var s settings.Settings
	err := envconfig.Process("UNLIKELY_PREFIX_", &s)
	if err != nil {
		panic(err)
	}

	// Set some convenient defaults for all integration tests.
	s.RuntimePath = "runtime/current"
	s.RuntimeSubdirectory = "ratelimit"
	s.RuntimeAppDirectory = "config"
	s.RedisPerSecondSocketType = "tcp"
	s.RedisSocketType = "tcp"
	s.DebugPort = 8084
	s.UseStatsd = false
	s.Port = 8082
	s.GrpcPort = 8083

	return s
}

func newDescriptorStatus(status pb.RateLimitResponse_Code, requestsPerUnit uint32, unit pb.RateLimitResponse_RateLimit_Unit, limitRemaining uint32, durRemaining *durationpb.Duration) *pb.RateLimitResponse_DescriptorStatus {
	limit := &pb.RateLimitResponse_RateLimit{RequestsPerUnit: requestsPerUnit, Unit: unit}

	return &pb.RateLimitResponse_DescriptorStatus{
		Code:               status,
		CurrentLimit:       limit,
		LimitRemaining:     limitRemaining,
		DurationUntilReset: &durationpb.Duration{Seconds: durRemaining.GetSeconds()},
	}
}

func makeSimpleRedisSettings(redisPort int, perSecondPort int, perSecond bool, localCacheSize int) settings.Settings {
	s := defaultSettings()

	s.RedisPerSecond = perSecond
	s.LocalCacheSizeInBytes = localCacheSize
	s.BackendType = "redis"

	// Use Docker Compose Redis Cluster if default port is specified
	if redisPort == 6379 {
		// Docker Compose Redis Cluster setup (3 masters)
		s.RedisType = "cluster"
		s.RedisUrl = "127.0.0.1:7001,127.0.0.1:7002,127.0.0.1:7003"

		if perSecond {
			s.RedisPerSecondType = "cluster"
			s.RedisPerSecondUrl = "127.0.0.1:7001,127.0.0.1:7002,127.0.0.1:7003"
		}
	} else {
		// Single Redis instance for other ports
		s.RedisUrl = "127.0.0.1:" + strconv.Itoa(redisPort)
		s.RedisPerSecondUrl = "127.0.0.1:" + strconv.Itoa(perSecondPort)
	}

	return s
}

func makeSimpleRedisSettingsWithStopCacheKeyIncrementWhenOverlimit(redisPort int, perSecondPort int, perSecond bool, localCacheSize int) settings.Settings {
	s := makeSimpleRedisSettings(redisPort, perSecondPort, perSecond, localCacheSize)

	s.StopCacheKeyIncrementWhenOverlimit = true
	return s
}

func TestBasicConfig(t *testing.T) {
	// Use Docker Compose Redis Cluster (ports 7001-7003)
	// WithMultiRedis will detect these ports are already open and just flush them
	common.WithMultiRedis(t, []common.RedisConfig{
		//{Port: 7001},
		//{Port: 7002},
		//{Port: 7003},
	}, func() {
		//t.Run("WithoutPerSecondRedis", testBasicConfig(makeSimpleRedisSettings(6379, 6379, false, 0)))
		//t.Run("WithPerSecondRedis", testBasicConfig(makeSimpleRedisSettings(6379, 6379, true, 0)))
		t.Run("WithoutPerSecondRedisWithLocalCache", testBasicConfigWithProcess(makeSimpleRedisSettings(6379, 6379, false, 1000)))
		//t.Run("WithPerSecondRedisWithLocalCache", testBasicConfig(makeSimpleRedisSettings(6379, 6379, true, 1000)))
		cacheSettings := makeSimpleRedisSettings(6379, 6379, false, 0)
		cacheSettings.CacheKeyPrefix = "prefix:"
		//t.Run("WithoutPerSecondRedisWithCachePrefix", testBasicConfig(cacheSettings))
		//t.Run("WithoutPerSecondRedisWithstopCacheKeyIncrementWhenOverlimitConfig", testBasicConfig(makeSimpleRedisSettingsWithStopCacheKeyIncrementWhenOverlimit(6379, 6379, false, 0)))
		//t.Run("WithPerSecondRedisWithstopCacheKeyIncrementWhenOverlimitConfig", testBasicConfig(makeSimpleRedisSettingsWithStopCacheKeyIncrementWhenOverlimit(6379, 6379, true, 0)))
		//t.Run("WithoutPerSecondRedisWithLocalCacheAndstopCacheKeyIncrementWhenOverlimitConfig", testBasicConfig(makeSimpleRedisSettingsWithStopCacheKeyIncrementWhenOverlimit(6379, 6379, false, 1000)))
		//t.Run("WithPerSecondRedisWithLocalCacheAndstopCacheKeyIncrementWhenOverlimitConfig", testBasicConfig(makeSimpleRedisSettingsWithStopCacheKeyIncrementWhenOverlimit(6379, 6379, true, 1000)))
	})
}

// TestBasicConfigWithSeparateProcess runs the ratelimit server in a separate process
func TestBasicConfigWithSeparateProcess(t *testing.T) {
	common.WithMultiRedis(t, []common.RedisConfig{
		{Port: 6379},
	}, func() {
		t.Run("WithoutPerSecondRedis", testBasicConfigWithProcess(makeSimpleRedisSettings(6379, 6379, false, 0)))
		t.Run("WithLocalCache", testBasicConfigWithProcess(makeSimpleRedisSettings(6379, 6379, false, 1000)))
	})
}

func TestXdsProviderBasicConfig(t *testing.T) {
	common.WithMultiRedis(t, []common.RedisConfig{
		{Port: 6379},
		{Port: 6379},
	}, func() {
		_, cancel := startXdsSotwServer(t)
		defer cancel()
		t.Run("WithoutPerSecondRedis", testXdsProviderBasicConfig(false, 0))
		t.Run("WithPerSecondRedis", testXdsProviderBasicConfig(true, 0))
		t.Run("WithoutPerSecondRedisWithLocalCache", testXdsProviderBasicConfig(false, 1000))
		t.Run("WithPerSecondRedisWithLocalCache", testXdsProviderBasicConfig(true, 1000))
	})
}

func TestBasicConfig_ExtraTags(t *testing.T) {
	common.WithMultiRedis(t, []common.RedisConfig{
		{Port: 6379},
	}, func() {
		extraTagsSettings := makeSimpleRedisSettings(6379, 6379, false, 0)
		extraTagsSettings.ExtraTags = map[string]string{"foo": "bar", "a": "b"}
		runner := startTestRunner(t, extraTagsSettings)
		defer runner.Stop()

		assert := assert.New(t)
		conn, err := grpc.Dial(fmt.Sprintf("localhost:%v", extraTagsSettings.GrpcPort), grpc.WithInsecure())
		assert.NoError(err)
		defer conn.Close()
		c := pb.NewRateLimitServiceClient(conn)

		_, err = c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest("basic", [][][2]string{{{getCacheKey("key1", false), "foo"}}}, 1))
		assert.NoError(err)

		// Manually flush the cache for local_cache stats
		runner.GetStatsStore().Flush()

		// store.NewCounter returns the existing counter.
		// This test looks for the extra tags requested.
		key1HitCounter := runner.GetStatsStore().NewCounterWithTags(
			fmt.Sprintf("ratelimit.service.rate_limit.basic.%s.total_hits", getCacheKey("key1", false)),
			extraTagsSettings.ExtraTags)
		assert.Equal(1, int(key1HitCounter.Value()))

		configLoadStat := runner.GetStatsStore().NewCounterWithTags(
			"ratelimit.service.config_load_success",
			extraTagsSettings.ExtraTags)
		assert.Equal(1, int(configLoadStat.Value()))

		// NOTE: This doesn't currently test that the extra tags are present for:
		// - local cache
		// - go runtime stats.
	})
}

func TestBasicTLSConfig(t *testing.T) {
	t.Run("WithoutPerSecondRedisTLS", testBasicConfigAuthTLS(false, 0))
	t.Run("WithPerSecondRedisTLS", testBasicConfigAuthTLS(true, 0))
	t.Run("WithoutPerSecondRedisTLSWithLocalCache", testBasicConfigAuthTLS(false, 1000))
	t.Run("WithPerSecondRedisTLSWithLocalCache", testBasicConfigAuthTLS(true, 1000))

	// Test using client cert.
	t.Run("WithoutPerSecondRedisTLSWithClientCert", testBasicConfigAuthTLSWithClientCert(false, 0))
}

func TestBasicAuthConfig(t *testing.T) {
	common.WithMultiRedis(t, []common.RedisConfig{
		{Port: 6384, Password: "password123"},
		{Port: 6385, Password: "password123"},
	}, func() {
		t.Run("WithoutPerSecondRedisAuth", testBasicConfigAuth(false, 0))
		t.Run("WithPerSecondRedisAuth", testBasicConfigAuth(true, 0))
		t.Run("WithoutPerSecondRedisAuthWithLocalCache", testBasicConfigAuth(false, 1000))
		t.Run("WithPerSecondRedisAuthWithLocalCache", testBasicConfigAuth(true, 1000))
	})
}

func TestBasicAuthConfigWithRedisCluster(t *testing.T) {
	t.Run("WithoutPerSecondRedisAuth", testBasicConfigAuthWithRedisCluster(false, 0))
	t.Run("WithPerSecondRedisAuth", testBasicConfigAuthWithRedisCluster(true, 0))
	t.Run("WithoutPerSecondRedisAuthWithLocalCache", testBasicConfigAuthWithRedisCluster(false, 1000))
	t.Run("WithPerSecondRedisAuthWithLocalCache", testBasicConfigAuthWithRedisCluster(true, 1000))
}

func TestBasicAuthConfigWithRedisSentinel(t *testing.T) {
	t.Run("WithoutPerSecondRedisAuth", testBasicAuthConfigWithRedisSentinel(false, 0))
	t.Run("WithPerSecondRedisAuth", testBasicAuthConfigWithRedisSentinel(true, 0))
	t.Run("WithoutPerSecondRedisAuthWithLocalCache", testBasicAuthConfigWithRedisSentinel(false, 1000))
	t.Run("WithPerSecondRedisAuthWithLocalCache", testBasicAuthConfigWithRedisSentinel(true, 1000))
}

func TestBasicReloadConfig(t *testing.T) {
	common.WithMultiRedis(t, []common.RedisConfig{
		{Port: 6379},
	}, func() {
		t.Run("BasicWithoutWatchRoot", testBasicConfigWithoutWatchRoot(false, 0))
		t.Run("ReloadWithoutWatchRoot", testBasicConfigReload(false, 0, false))
	})
}

func TestXdsProviderBasicConfigReload(t *testing.T) {
	common.WithMultiRedis(t, []common.RedisConfig{
		{Port: 6379},
	}, func() {
		setSnapshotFunc, cancel := startXdsSotwServer(t)
		defer cancel()

		t.Run("ReloadConfigWithXdsServer", testXdsProviderBasicConfigReload(setSnapshotFunc, false, 0))
	})
}

func makeSimpleMemcacheSettings(memcachePorts []int, localCacheSize int) settings.Settings {
	s := defaultSettings()
	var memcacheHostAndPort []string
	for _, memcachePort := range memcachePorts {
		memcacheHostAndPort = append(memcacheHostAndPort, "localhost:"+strconv.Itoa(memcachePort))
	}
	s.MemcacheHostPort = memcacheHostAndPort
	s.LocalCacheSizeInBytes = localCacheSize
	s.BackendType = "memcache"
	return s
}

func TestBasicConfigMemcache(t *testing.T) {
	singleNodePort := []int{6394}
	common.WithMultiMemcache(t, []common.MemcacheConfig{
		{Port: 6394},
	}, func() {
		t.Run("Memcache", testBasicConfig(makeSimpleMemcacheSettings(singleNodePort, 0)))
		t.Run("MemcacheWithLocalCache", testBasicConfig(makeSimpleMemcacheSettings(singleNodePort, 1000)))
		cacheSettings := makeSimpleMemcacheSettings(singleNodePort, 0)
		cacheSettings.CacheKeyPrefix = "prefix:"
		t.Run("MemcacheWithPrefix", testBasicConfig(cacheSettings))
	})
}

func TestConfigMemcacheWithMaxIdleConns(t *testing.T) {
	singleNodePort := []int{6394}
	assert := assert.New(t)
	common.WithMultiMemcache(t, []common.MemcacheConfig{
		{Port: 6394},
	}, func() {
		withDefaultMaxIdleConns := makeSimpleMemcacheSettings(singleNodePort, 0)
		assert.Equal(2, withDefaultMaxIdleConns.MemcacheMaxIdleConns)
		t.Run("MemcacheWithDefaultMaxIdleConns", testBasicConfig(withDefaultMaxIdleConns))
		withSpecifiedMaxIdleConns := makeSimpleMemcacheSettings(singleNodePort, 0)
		withSpecifiedMaxIdleConns.MemcacheMaxIdleConns = 100
		t.Run("MemcacheWithSpecifiedMaxIdleConns", testBasicConfig(withSpecifiedMaxIdleConns))
	})
}

func TestMultiNodeMemcache(t *testing.T) {
	multiNodePorts := []int{6494, 6495}
	common.WithMultiMemcache(t, []common.MemcacheConfig{
		{Port: 6494}, {Port: 6495},
	}, func() {
		t.Run("MemcacheMultipleNodes", testBasicConfig(makeSimpleMemcacheSettings(multiNodePorts, 0)))
	})
}

func Test_mTLS(t *testing.T) {
	s := makeSimpleRedisSettings(16381, 16382, false, 0)
	s.RedisTlsConfig = &tls.Config{}
	s.RedisAuth = "password123"
	s.RedisTls = true
	s.RedisTlsSkipHostnameVerification = false
	s.RedisPerSecondAuth = "password123"
	s.RedisPerSecondTls = true
	assert := assert.New(t)
	serverCAFile, serverCertFile, serverCertKey, err := mTLSSetup(utils.ServerCA)
	assert.NoError(err)
	clientCAFile, clientCertFile, clientCertKey, err := mTLSSetup(utils.ClientCA)
	assert.NoError(err)
	s.GrpcServerUseTLS = true
	s.GrpcServerTlsCert = serverCertFile
	s.GrpcServerTlsKey = serverCertKey
	s.GrpcClientTlsCACert = clientCAFile
	s.GrpcClientTlsSAN = "localhost"
	settings.GrpcServerTlsConfig()(&s)
	runner := startTestRunner(t, s)
	defer runner.Stop()
	clientTlsConfig := utils.TlsConfigFromFiles(clientCertFile, clientCertKey, serverCAFile, utils.ServerCA, false)
	conn, err := grpc.Dial(fmt.Sprintf("localhost:%v", s.GrpcPort), grpc.WithTransportCredentials(credentials.NewTLS(clientTlsConfig)))
	assert.NoError(err)
	defer conn.Close()
}

func TestReloadGRPCServerCerts(t *testing.T) {
	common.WithMultiRedis(t, []common.RedisConfig{
		{Port: 6379},
	}, func() {
		s := makeSimpleRedisSettings(6379, 6379, false, 0)
		assert := assert.New(t)
		// TLS setup initially used to configure the server
		initialServerCAFile, initialServerCertFile, initialServerCertKey, err := mTLSSetup(utils.ServerCA)
		assert.NoError(err)
		// Second TLS setup that will replace the above during test
		newServerCAFile, newServerCertFile, newServerCertKey, err := mTLSSetup(utils.ServerCA)
		assert.NoError(err)
		// Create CertPools and tls.Configs for both CAs
		initialCaCert, err := os.ReadFile(initialServerCAFile)
		assert.NoError(err)
		initialCertPool := x509.NewCertPool()
		initialCertPool.AppendCertsFromPEM(initialCaCert)
		initialTlsConfig := &tls.Config{
			RootCAs: initialCertPool,
		}
		newCaCert, err := os.ReadFile(newServerCAFile)
		assert.NoError(err)
		newCertPool := x509.NewCertPool()
		newCertPool.AppendCertsFromPEM(newCaCert)
		newTlsConfig := &tls.Config{
			RootCAs: newCertPool,
		}
		connStr := fmt.Sprintf("localhost:%v", s.GrpcPort)

		// Set up ratelimit with the initial certificate
		s.GrpcServerUseTLS = true
		s.GrpcServerTlsCert = initialServerCertFile
		s.GrpcServerTlsKey = initialServerCertKey
		settings.GrpcServerTlsConfig()(&s)
		runner := startTestRunner(t, s)
		defer runner.Stop()

		// Ensure TLS validation works with the initial CA in cert pool
		t.Run("WithInitialCert", func(t *testing.T) {
			conn, err := tls.Dial("tcp", connStr, initialTlsConfig)
			assert.NoError(err)
			conn.Close()
		})

		// Ensure TLS validation fails with the new CA in cert pool
		t.Run("WithNewCertFail", func(t *testing.T) {
			conn, err := tls.Dial("tcp", connStr, newTlsConfig)
			assert.Error(err)
			if err == nil {
				conn.Close()
			}
		})

		// Replace the initial certificate with the new one
		err = os.Rename(newServerCertFile, initialServerCertFile)
		assert.NoError(err)
		err = os.Rename(newServerCertKey, initialServerCertKey)
		assert.NoError(err)

		// Ensure TLS validation works with the new CA in cert pool
		t.Run("WithNewCertOK", func(t *testing.T) {
			// If this takes longer than 10s, something is probably wrong
			wait := 10
			for i := 0; i < wait; i++ {
				// Ensure the new certificate is being used
				conn, err := tls.Dial("tcp", connStr, newTlsConfig)
				if err == nil {
					conn.Close()
					break
				}
				time.Sleep(1 * time.Second)
			}
			assert.NoError(err)
		})

		// Ensure TLS validation fails with the initial CA in cert pool
		t.Run("WithInitialCertFail", func(t *testing.T) {
			conn, err := tls.Dial("tcp", connStr, initialTlsConfig)
			assert.Error(err)
			if err == nil {
				conn.Close()
			}
		})
	})
}

func testBasicConfigAuthTLS(perSecond bool, local_cache_size int) func(*testing.T) {
	s := makeSimpleRedisSettings(16381, 16382, perSecond, local_cache_size)
	s.RedisTlsConfig = &tls.Config{}
	s.RedisAuth = "password123"
	s.RedisTls = true
	s.RedisPerSecondAuth = "password123"
	s.RedisPerSecondTls = true

	return testBasicBaseConfig(s)
}

func testBasicConfigAuthTLSWithClientCert(perSecond bool, local_cache_size int) func(*testing.T) {
	// "16361" is the port of the redis server running behind stunnel with verify level 2 (the level 2
	// verifies the peer certificate against the defined CA certificate (CAfile)).
	// See: Makefile#REDIS_VERIFY_PEER_STUNNEL.
	s := makeSimpleRedisSettings(16361, 16382, perSecond, local_cache_size)
	s.RedisTlsClientCert = filepath.Join(projectDir, "cert.pem")
	s.RedisTlsClientKey = filepath.Join(projectDir, "key.pem")
	s.RedisTlsCACert = filepath.Join(projectDir, "cert.pem")
	s.RedisTls = true
	s.RedisPerSecondTls = true
	settings.RedisTlsConfig(s.RedisTls || s.RedisPerSecondTls)(&s)
	s.RedisAuth = "password123"
	s.RedisPerSecondAuth = "password123"

	return testBasicBaseConfig(s)
}

func testBasicConfig(s settings.Settings) func(*testing.T) {
	return testBasicBaseConfig(s)
}

// testBasicConfigWithProcess runs the test with ratelimit server in a separate process
func testBasicConfigWithProcess(s settings.Settings) func(*testing.T) {
	return func(t *testing.T) {
		enableLocalCache := s.LocalCacheSizeInBytes > 0
		runner := startTestRunnerProcess(t, s)
		defer runner.Stop()

		assert := assert.New(t)
		conn, err := grpc.Dial(fmt.Sprintf("localhost:%v", s.GrpcPort), grpc.WithInsecure())
		assert.NoError(err)
		defer conn.Close()
		c := pb.NewRateLimitServiceClient(conn)

		// Test 1: Unknown domain returns OK with no limit
		response, err := c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest("foo", [][][2]string{{{getCacheKey("hello", enableLocalCache), "world"}}}, 1))
		common.AssertProtoEqual(
			assert,
			&pb.RateLimitResponse{
				OverallCode: pb.RateLimitResponse_OK,
				Statuses:    []*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0}},
			},
			response)
		assert.NoError(err)

		// Test 2: Known domain with limit
		response, err = c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest("basic", [][][2]string{{{getCacheKey("key1", enableLocalCache), "foo"}}}, 1))
		assert.NoError(err)
		require.NotEmpty(t, response.GetStatuses())
		durRemaining := response.GetStatuses()[0].DurationUntilReset
		common.AssertProtoEqual(
			assert,
			&pb.RateLimitResponse{
				OverallCode: pb.RateLimitResponse_OK,
				Statuses: []*pb.RateLimitResponse_DescriptorStatus{
					newDescriptorStatus(pb.RateLimitResponse_OK, 50, pb.RateLimitResponse_RateLimit_SECOND, 49, durRemaining),
				},
			},
			response)

		// Test 3: Send 25 async requests and verify rate limiting behavior
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		randomInt := r.Int()

		type asyncResult struct {
			response *pb.RateLimitResponse
			err      error
			duration time.Duration
		}

		const numRequests = 200
		results := make([]asyncResult, numRequests)
		var wg sync.WaitGroup
		wg.Add(numRequests)

		startTime := time.Now()
		for i := 0; i < numRequests; i++ {
			go func(idx int) {
				defer wg.Done()
				reqStart := time.Now()

				resp, reqErr := c.ShouldRateLimit(
					context.Background(),
					common.NewRateLimitRequest(
						"another", [][][2]string{
							{{getCacheKey("key2", enableLocalCache), strconv.Itoa(randomInt)}},
							{{getCacheKey("key3", enableLocalCache), strconv.Itoa(randomInt)}},
							{{getCacheKey("key4", enableLocalCache), strconv.Itoa(randomInt)}},
							{{getCacheKey("key5", enableLocalCache), strconv.Itoa(randomInt)}},
							{{getCacheKey("key6", enableLocalCache), strconv.Itoa(randomInt)}},
						}, 1))
				results[idx] = asyncResult{
					response: resp,
					err:      reqErr,
					duration: time.Since(reqStart),
				}
			}(i)
		}
		wg.Wait()
		totalDuration := time.Since(startTime)

		// Verify all requests completed without error
		okCount := 0
		overLimitCount := 0
		for i := 0; i < numRequests; i++ {
			assert.NoError(results[i].err)
			if results[i].response.OverallCode == pb.RateLimitResponse_OK {
				okCount++
			} else if results[i].response.OverallCode == pb.RateLimitResponse_OVER_LIMIT {
				overLimitCount++
			}
		}

		// With limit of 20, we expect 20 OK and 5 OVER_LIMIT
		assert.Equal(10, okCount, "Expected 100 OK responses")
		assert.Equal(190, overLimitCount, "Expected 5 OVER_LIMIT responses")

		t.Logf("Separate process test: Total time for %d async requests: %v (avg: %v)", numRequests, totalDuration, totalDuration/time.Duration(numRequests))
	}
}

func testBasicConfigAuth(perSecond bool, local_cache_size int) func(*testing.T) {
	s := makeSimpleRedisSettings(6384, 6385, perSecond, local_cache_size)
	s.RedisAuth = "password123"
	s.RedisPerSecondAuth = "password123"

	return testBasicBaseConfig(s)
}

func testBasicConfigAuthWithRedisCluster(perSecond bool, local_cache_size int) func(*testing.T) {
	s := defaultSettings()

	s.RedisPerSecond = perSecond
	s.LocalCacheSizeInBytes = local_cache_size
	s.BackendType = "redis"

	configRedisCluster(&s)

	return testBasicBaseConfig(s)
}

func configRedisSentinel(s *settings.Settings) {
	s.RedisPerSecondType = "sentinel"

	s.RedisPerSecondUrl = "mymaster,localhost:26399,localhost:26400,localhost:26401"
	s.RedisType = "sentinel"
	s.RedisUrl = "mymaster,localhost:26394,localhost:26395,localhost:26396"
	s.RedisAuth = "password123"
	s.RedisPerSecondAuth = "password123"
}

func testBasicAuthConfigWithRedisSentinel(perSecond bool, local_cache_size int) func(*testing.T) {
	s := defaultSettings()

	s.RedisPerSecond = perSecond
	s.LocalCacheSizeInBytes = local_cache_size
	s.BackendType = "redis"

	configRedisSentinel(&s)

	return testBasicBaseConfig(s)
}

func testBasicConfigWithoutWatchRoot(perSecond bool, local_cache_size int) func(*testing.T) {
	s := makeSimpleRedisSettings(6379, 6379, perSecond, local_cache_size)
	s.RuntimeWatchRoot = false

	return testBasicBaseConfig(s)
}

func configRedisCluster(s *settings.Settings) {
	s.RedisPerSecondType = "cluster"
	s.RedisPerSecondUrl = "localhost:6389,localhost:6390,localhost:6391"
	s.RedisType = "cluster"
	s.RedisUrl = "localhost:6386,localhost:6387,localhost:6388"

	s.RedisAuth = "password123"
	s.RedisPerSecondAuth = "password123"
}

func testBasicConfigWithoutWatchRootWithRedisCluster(perSecond bool, local_cache_size int) func(*testing.T) {
	s := defaultSettings()

	s.RedisPerSecond = perSecond
	s.LocalCacheSizeInBytes = local_cache_size
	s.BackendType = "redis"

	s.RuntimeWatchRoot = false

	configRedisCluster(&s)

	return testBasicBaseConfig(s)
}

func testBasicConfigWithoutWatchRootWithRedisSentinel(perSecond bool, local_cache_size int) func(*testing.T) {
	s := defaultSettings()

	s.RedisPerSecond = perSecond
	s.LocalCacheSizeInBytes = local_cache_size
	s.BackendType = "redis"

	configRedisSentinel(&s)

	s.RuntimeWatchRoot = false

	return testBasicBaseConfig(s)
}

func testBasicConfigReload(perSecond bool, local_cache_size int, runtimeWatchRoot bool) func(*testing.T) {
	s := makeSimpleRedisSettings(6379, 6379, perSecond, local_cache_size)
	s.RuntimeWatchRoot = runtimeWatchRoot
	return testConfigReload(s, reloadNewConfigFile, restoreConfigFile)
}

func testBasicConfigReloadWithRedisCluster(perSecond bool, local_cache_size int, runtimeWatchRoot string) func(*testing.T) {
	s := defaultSettings()

	s.RedisPerSecond = perSecond
	s.LocalCacheSizeInBytes = local_cache_size
	s.BackendType = "redis"

	s.RuntimeWatchRoot = s.RuntimeWatchRoot

	configRedisCluster(&s)

	return testConfigReload(s, reloadNewConfigFile, restoreConfigFile)
}

func testBasicConfigReloadWithRedisSentinel(perSecond bool, local_cache_size int, runtimeWatchRoot bool) func(*testing.T) {
	s := defaultSettings()

	s.RedisPerSecond = perSecond
	s.LocalCacheSizeInBytes = local_cache_size
	s.BackendType = "redis"

	configRedisSentinel(&s)

	s.RuntimeWatchRoot = runtimeWatchRoot

	return testConfigReload(s, reloadNewConfigFile, restoreConfigFile)
}

func getCacheKey(cacheKey string, enableLocalCache bool) string {
	if enableLocalCache {
		return cacheKey + "_local"
	}

	return cacheKey
}

func testBasicBaseConfig(s settings.Settings) func(*testing.T) {
	return func(t *testing.T) {
		enable_local_cache := s.LocalCacheSizeInBytes > 0
		runner := startTestRunner(t, s)
		defer runner.Stop()

		assert := assert.New(t)
		conn, err := grpc.Dial(fmt.Sprintf("localhost:%v", s.GrpcPort), grpc.WithInsecure())
		assert.NoError(err)
		defer conn.Close()
		c := pb.NewRateLimitServiceClient(conn)

		response, err := c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest("foo", [][][2]string{{{getCacheKey("hello", enable_local_cache), "world"}}}, 1))
		common.AssertProtoEqual(
			assert,
			&pb.RateLimitResponse{
				OverallCode: pb.RateLimitResponse_OK,
				Statuses:    []*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK, CurrentLimit: nil, LimitRemaining: 0}},
			},
			response)
		assert.NoError(err)

		// Manually flush the cache for local_cache stats
		runner.GetStatsStore().Flush()
		localCacheHitCounter := runner.GetStatsStore().NewGauge("ratelimit.localcache.hitCount")
		assert.Equal(0, int(localCacheHitCounter.Value()))

		localCacheMissCounter := runner.GetStatsStore().NewGauge("ratelimit.localcache.missCount")
		assert.Equal(0, int(localCacheMissCounter.Value()))

		response, err = c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest("basic", [][][2]string{{{getCacheKey("key1", enable_local_cache), "foo"}}}, 1))
		assert.NoError(err)
		require.NotEmpty(t, response.GetStatuses())
		durRemaining := response.GetStatuses()[0].DurationUntilReset

		common.AssertProtoEqual(
			assert,
			&pb.RateLimitResponse{
				OverallCode: pb.RateLimitResponse_OK,
				Statuses: []*pb.RateLimitResponse_DescriptorStatus{
					newDescriptorStatus(pb.RateLimitResponse_OK, 50, pb.RateLimitResponse_RateLimit_SECOND, 49, durRemaining),
				},
			},
			response)
		assert.NoError(err)

		// store.NewCounter returns the existing counter.
		key1HitCounter := runner.GetStatsStore().NewCounter(fmt.Sprintf("ratelimit.service.rate_limit.basic.%s.total_hits", getCacheKey("key1", enable_local_cache)))
		assert.Equal(1, int(key1HitCounter.Value()))

		// Manually flush the cache for local_cache stats
		runner.GetStatsStore().Flush()
		localCacheHitCounter = runner.GetStatsStore().NewGauge("ratelimit.localcache.hitCount")
		assert.Equal(0, int(localCacheHitCounter.Value()))

		localCacheMissCounter = runner.GetStatsStore().NewGauge("ratelimit.localcache.missCount")
		if enable_local_cache {
			assert.Equal(1, int(localCacheMissCounter.Value()))
		} else {
			assert.Equal(0, int(localCacheMissCounter.Value()))
		}

		// Now come up with a random key, and go over limit for a minute limit which should always work.
		// Send requests asynchronously using goroutines
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		randomInt := r.Int()

		type asyncResult struct {
			response *pb.RateLimitResponse
			err      error
			duration time.Duration
		}

		const numRequests = 25
		results := make([]asyncResult, numRequests)
		var wg sync.WaitGroup
		wg.Add(numRequests)

		startTime := time.Now()
		for i := 0; i < numRequests; i++ {
			go func(idx int) {
				defer wg.Done()
				reqStart := time.Now()
				resp, reqErr := c.ShouldRateLimit(
					context.Background(),
					common.NewRateLimitRequest(
						"another", [][][2]string{{{getCacheKey("key2", enable_local_cache), strconv.Itoa(randomInt)}}}, 1))
				results[idx] = asyncResult{
					response: resp,
					err:      reqErr,
					duration: time.Since(reqStart),
				}
			}(i)
		}
		wg.Wait()
		totalDuration := time.Since(startTime)

		// Verify all requests completed without error
		okCount := 0
		overLimitCount := 0
		for i := 0; i < numRequests; i++ {
			assert.NoError(results[i].err)
			if results[i].response.OverallCode == pb.RateLimitResponse_OK {
				okCount++
			} else if results[i].response.OverallCode == pb.RateLimitResponse_OVER_LIMIT {
				overLimitCount++
			}
		}

		// With limit of 20, we expect 20 OK and 5 OVER_LIMIT
		assert.Equal(20, okCount, "Expected 20 OK responses")
		assert.Equal(5, overLimitCount, "Expected 5 OVER_LIMIT responses")

		// Verify counters after all async requests complete
		key2HitCounter := runner.GetStatsStore().NewCounter(fmt.Sprintf("ratelimit.service.rate_limit.another.%s.total_hits", getCacheKey("key2", enable_local_cache)))
		assert.Equal(numRequests, int(key2HitCounter.Value()))
		key2OverlimitCounter := runner.GetStatsStore().NewCounter(fmt.Sprintf("ratelimit.service.rate_limit.another.%s.over_limit", getCacheKey("key2", enable_local_cache)))
		assert.Equal(5, int(key2OverlimitCounter.Value()))

		t.Logf("Total time for %d async requests: %v (avg: %v)", numRequests, totalDuration, totalDuration/time.Duration(numRequests))

		// Limit now against 2 keys in the same domain.
		// Send requests asynchronously using goroutines
		randomInt = r.Int()

		const numRequests2Keys = 15
		results2Keys := make([]asyncResult, numRequests2Keys)
		var wg2 sync.WaitGroup
		wg2.Add(numRequests2Keys)

		startTime2 := time.Now()
		for i := 0; i < numRequests2Keys; i++ {
			go func(idx int) {
				defer wg2.Done()
				reqStart := time.Now()
				resp, reqErr := c.ShouldRateLimit(
					context.Background(),
					common.NewRateLimitRequest(
						"another",
						[][][2]string{
							{{getCacheKey("key2", enable_local_cache), strconv.Itoa(randomInt)}},
							{{getCacheKey("key3", enable_local_cache), strconv.Itoa(randomInt)}},
						}, 1))
				results2Keys[idx] = asyncResult{
					response: resp,
					err:      reqErr,
					duration: time.Since(reqStart),
				}
			}(i)
		}
		wg2.Wait()
		totalDuration2 := time.Since(startTime2)

		// Verify all requests completed without error
		okCount2 := 0
		overLimitCount2 := 0
		for i := 0; i < numRequests2Keys; i++ {
			assert.NoError(results2Keys[i].err)
			if results2Keys[i].response.OverallCode == pb.RateLimitResponse_OK {
				okCount2++
			} else if results2Keys[i].response.OverallCode == pb.RateLimitResponse_OVER_LIMIT {
				overLimitCount2++
			}
		}

		// With key3 limit of 10, we expect 10 OK and 5 OVER_LIMIT
		assert.Equal(10, okCount2, "Expected 10 OK responses for 2-key requests")
		assert.Equal(5, overLimitCount2, "Expected 5 OVER_LIMIT responses for 2-key requests")

		// Verify counters after all async requests complete
		key2HitCounter = runner.GetStatsStore().NewCounter(fmt.Sprintf("ratelimit.service.rate_limit.another.%s.total_hits", getCacheKey("key2", enable_local_cache)))
		assert.Equal(numRequests+numRequests2Keys, int(key2HitCounter.Value()))

		key3HitCounter := runner.GetStatsStore().NewCounter(fmt.Sprintf("ratelimit.service.rate_limit.another.%s.total_hits", getCacheKey("key3", enable_local_cache)))
		assert.Equal(numRequests2Keys, int(key3HitCounter.Value()))

		key3OverlimitCounter := runner.GetStatsStore().NewCounter(fmt.Sprintf("ratelimit.service.rate_limit.another.%s.over_limit", getCacheKey("key3", enable_local_cache)))
		assert.Equal(5, int(key3OverlimitCounter.Value()))

		t.Logf("Total time for %d async requests (2 keys): %v (avg: %v)", numRequests2Keys, totalDuration2, totalDuration2/time.Duration(numRequests2Keys))

		// Test DurationUntilReset by hitting same key twice
		resp1, err := c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest("another", [][][2]string{{{getCacheKey("key4", enable_local_cache), "durTest"}}}, 1))

		time.Sleep(2 * time.Second) // Wait to allow duration to tick down

		resp2, err := c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest("another", [][][2]string{{{getCacheKey("key4", enable_local_cache), "durTest"}}}, 1))

		assert.Less(resp2.GetStatuses()[0].DurationUntilReset.GetSeconds(), resp1.GetStatuses()[0].DurationUntilReset.GetSeconds())
	}
}

func startTestRunner(t *testing.T, s settings.Settings) *runner.Runner {
	t.Helper()
	runner := runner.NewRunner(s)

	go func() {
		// Catch a panic() to ensure that test name is printed.
		// Otherwise go doesn't know what test this goroutine is
		// associated with.
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Uncaught panic(): %v", r)
			}
		}()
		runner.Run()
	}()

	// HACK: Wait for the server to come up. Make a hook that we can wait on.
	common.WaitForTcpPort(context.Background(), s.GrpcPort, 1*time.Second)

	return &runner
}

// ProcessRunner wraps a separate process running the ratelimit server
type ProcessRunner struct {
	cmd      *exec.Cmd
	t        *testing.T
	grpcPort int
}

// Stop terminates the ratelimit server process
func (p *ProcessRunner) Stop() {
	if p.cmd != nil && p.cmd.Process != nil {
		// Send SIGTERM for graceful shutdown
		p.cmd.Process.Signal(syscall.SIGTERM)

		// Wait for process to exit with timeout
		done := make(chan error, 1)
		go func() {
			done <- p.cmd.Wait()
		}()

		select {
		case <-done:
			// Process exited
		case <-time.After(5 * time.Second):
			// Force kill if not exited
			p.cmd.Process.Kill()
		}
	}
}

// startTestRunnerProcess starts the ratelimit server as a separate process
func startTestRunnerProcess(t *testing.T, s settings.Settings) *ProcessRunner {
	t.Helper()

	// Build the ratelimit binary first
	buildCmd := exec.Command("go", "build", "-o", "/tmp/ratelimit_test_server", "/Users/seonghyun/GolandProjects/ratelimit/src/service_cmd/main.go")
	buildCmd.Dir = projectDir
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to build ratelimit server: %v\nOutput: %s", err, output)
	}

	// Prepare environment variables based on settings
	env := os.Environ()
	env = append(env, fmt.Sprintf("PORT=%d", s.Port))
	env = append(env, fmt.Sprintf("GRPC_PORT=%d", s.GrpcPort))
	env = append(env, fmt.Sprintf("DEBUG_PORT=%d", s.DebugPort))
	env = append(env, fmt.Sprintf("RUNTIME_ROOT=%s", s.RuntimePath))
	env = append(env, fmt.Sprintf("RUNTIME_SUBDIRECTORY=%s", s.RuntimeSubdirectory))
	env = append(env, fmt.Sprintf("RUNTIME_APPDIRECTORY=%s", s.RuntimeAppDirectory))
	env = append(env, fmt.Sprintf("USE_STATSD=%t", s.UseStatsd))
	env = append(env, fmt.Sprintf("BACKEND_TYPE=%s", s.BackendType))
	env = append(env, fmt.Sprintf("REDIS_SOCKET_TYPE=%s", s.RedisSocketType))
	env = append(env, fmt.Sprintf("REDIS_URL=%s", s.RedisUrl))
	env = append(env, fmt.Sprintf("REDIS_PERSECOND=%t", s.RedisPerSecond))
	env = append(env, fmt.Sprintf("REDIS_PERSECOND_SOCKET_TYPE=%s", s.RedisPerSecondSocketType))
	env = append(env, fmt.Sprintf("REDIS_PERSECOND_URL=%s", s.RedisPerSecondUrl))
	env = append(env, fmt.Sprintf("LOCAL_CACHE_SIZE_IN_BYTES=%d", s.LocalCacheSizeInBytes))

	if s.RedisAuth != "" {
		env = append(env, fmt.Sprintf("REDIS_AUTH=%s", s.RedisAuth))
	}
	if s.RedisPerSecondAuth != "" {
		env = append(env, fmt.Sprintf("REDIS_PERSECOND_AUTH=%s", s.RedisPerSecondAuth))
	}
	if s.CacheKeyPrefix != "" {
		env = append(env, fmt.Sprintf("CACHE_KEY_PREFIX=%s", s.CacheKeyPrefix))
	}

	// Start the server process
	cmd := exec.Command("/tmp/ratelimit_test_server")
	cmd.Dir = projectDir
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start ratelimit server process: %v", err)
	}

	t.Logf("Started ratelimit server process with PID: %d", cmd.Process.Pid)

	// Wait for the server to come up
	common.WaitForTcpPort(context.Background(), s.GrpcPort, 5*time.Second)
	return &ProcessRunner{
		cmd:      cmd,
		t:        t,
		grpcPort: s.GrpcPort,
	}
}

func testConfigReload(s settings.Settings, reloadConfFunc, restoreConfFunc func()) func(*testing.T) {
	return func(t *testing.T) {
		enable_local_cache := s.LocalCacheSizeInBytes > 0
		runner := startTestRunner(t, s)
		defer runner.Stop()

		assert := assert.New(t)
		conn, err := grpc.Dial(fmt.Sprintf("localhost:%v", s.GrpcPort), grpc.WithInsecure())
		assert.NoError(err)
		defer conn.Close()
		c := pb.NewRateLimitServiceClient(conn)

		response, err := c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest("reload", [][][2]string{{{getCacheKey("block", enable_local_cache), "foo"}}}, 1))
		common.AssertProtoEqual(
			assert,
			&pb.RateLimitResponse{
				OverallCode: pb.RateLimitResponse_OK,
				Statuses:    []*pb.RateLimitResponse_DescriptorStatus{{Code: pb.RateLimitResponse_OK}},
			},
			response)
		assert.NoError(err)

		runner.GetStatsStore().Flush()
		loadCountBefore := runner.GetStatsStore().NewCounter("ratelimit.service.config_load_success").Value()

		reloadConfFunc()
		loadCountAfter, reloaded := waitForConfigReload(runner, loadCountBefore)

		assert.True(reloaded)
		assert.Greater(loadCountAfter, loadCountBefore)

		response, err = c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest("reload", [][][2]string{{{getCacheKey("key1", enable_local_cache), "foo"}}}, 1))

		durRemaining := response.GetStatuses()[0].DurationUntilReset
		common.AssertProtoEqual(
			assert,
			&pb.RateLimitResponse{
				OverallCode: pb.RateLimitResponse_OK,
				Statuses: []*pb.RateLimitResponse_DescriptorStatus{
					newDescriptorStatus(pb.RateLimitResponse_OK, 50, pb.RateLimitResponse_RateLimit_SECOND, 49, durRemaining),
				},
			},
			response)
		assert.NoError(err)

		restoreConfFunc()
		// Removal of config files must trigger a reload
		loadCountBefore = loadCountAfter
		loadCountAfter, reloaded = waitForConfigReload(runner, loadCountBefore)
		assert.True(reloaded)
		assert.Greater(loadCountAfter, loadCountBefore)
	}
}

func reloadNewConfigFile() {
	// Copy a new file to config folder to test config reload functionality
	in, err := os.Open("runtime/current/ratelimit/reload.yaml")
	if err != nil {
		panic(err)
	}
	defer in.Close()
	out, err := os.Create("runtime/current/ratelimit/config/reload.yaml")
	if err != nil {
		panic(err)
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	if err != nil {
		panic(err)
	}
	err = out.Close()
	if err != nil {
		panic(err)
	}
}

func restoreConfigFile() {
	err := os.Remove("runtime/current/ratelimit/config/reload.yaml")
	if err != nil {
		panic(err)
	}
}

func waitForConfigReload(runner *runner.Runner, loadCountBefore uint64) (uint64, bool) {
	// Need to wait for config reload to take place and new descriptors to be loaded.
	// Shouldn't take more than 5 seconds but wait 120 at most just to be safe.
	wait := 120
	reloaded := false
	loadCountAfter := uint64(0)

	for i := 0; i < wait; i++ {
		time.Sleep(1 * time.Second)
		runner.GetStatsStore().Flush()
		loadCountAfter = runner.GetStatsStore().NewCounter("ratelimit.service.config_load_success").Value()

		// Check that successful loads count has increased before continuing.
		if loadCountAfter > loadCountBefore {
			reloaded = true
			break
		}
	}
	return loadCountAfter, reloaded
}

func TestShareThreshold(t *testing.T) {
	common.WithMultiRedis(t, []common.RedisConfig{
		{Port: 6379},
		{Port: 6379},
	}, func() {
		t.Run("WithoutPerSecondRedis", testShareThreshold(makeSimpleRedisSettings(6379, 6379, false, 0)))
	})
}

func testShareThreshold(s settings.Settings) func(*testing.T) {
	return func(t *testing.T) {
		runner := startTestRunner(t, s)
		defer runner.Stop()

		assert := assert.New(t)
		conn, err := grpc.Dial(fmt.Sprintf("localhost:%v", s.GrpcPort), grpc.WithInsecure())
		assert.NoError(err)
		defer conn.Close()
		c := pb.NewRateLimitServiceClient(conn)

		// Use the domain from the config file
		domain := "share-threshold-test"

		// Test Case 1: share_threshold: true - different values matching files/* should share the same threshold
		// Make 10 requests with files/a.pdf - can be OK or OVER_LIMIT
		for i := 0; i < 10; i++ {
			response, err := c.ShouldRateLimit(
				context.Background(),
				common.NewRateLimitRequest(domain, [][][2]string{{{"files", "files/a.pdf"}}}, 1))
			assert.NoError(err)
			// Each request can be OK or OVER_LIMIT (depending on when limit is reached)
			assert.True(response.OverallCode == pb.RateLimitResponse_OK || response.OverallCode == pb.RateLimitResponse_OVER_LIMIT,
				"Request %d should be OK or OVER_LIMIT, got: %v", i+1, response.OverallCode)
		}

		// Now make a request with files/b.csv - must be OVER_LIMIT because it shares the threshold
		response, err := c.ShouldRateLimit(
			context.Background(),
			common.NewRateLimitRequest(domain, [][][2]string{{{"files", "files/b.csv"}}}, 1))
		assert.NoError(err)
		durRemaining := response.GetStatuses()[0].DurationUntilReset
		common.AssertProtoEqual(
			assert,
			&pb.RateLimitResponse{
				OverallCode: pb.RateLimitResponse_OVER_LIMIT,
				Statuses: []*pb.RateLimitResponse_DescriptorStatus{
					newDescriptorStatus(pb.RateLimitResponse_OVER_LIMIT, 10, pb.RateLimitResponse_RateLimit_HOUR, 0, durRemaining),
				},
			},
			response)

		// Test Case 2: share_threshold: false - different values should have isolated thresholds
		// Use random values with prefix files_no_share to ensure uniqueness (based on timestamp)
		// Each value should have its own isolated threshold, so all 10 requests should be OK
		baseTimestamp := time.Now().UnixNano()
		r := rand.New(rand.NewSource(baseTimestamp))
		for i := 0; i < 10; i++ {
			// Generate unique value using timestamp and random number to avoid collisions
			uniqueValue := fmt.Sprintf("files_no_share/%d-%d", baseTimestamp, r.Int63())
			response, err := c.ShouldRateLimit(
				context.Background(),
				common.NewRateLimitRequest(domain, [][][2]string{{{"files_no_share", uniqueValue}}}, 1))
			assert.NoError(err)
			// Each value has its own isolated threshold, so each request should have remaining = 9 (10 - 1)
			expectedRemaining := uint32(9)
			durRemaining := response.GetStatuses()[0].DurationUntilReset
			common.AssertProtoEqual(
				assert,
				&pb.RateLimitResponse{
					OverallCode: pb.RateLimitResponse_OK,
					Statuses: []*pb.RateLimitResponse_DescriptorStatus{
						newDescriptorStatus(pb.RateLimitResponse_OK, 10, pb.RateLimitResponse_RateLimit_HOUR, expectedRemaining, durRemaining),
					},
				},
				response)
		}
	}
}

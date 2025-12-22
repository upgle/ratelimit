# Redis Hotkey 관리 분석

## 개요

현재 ratelimit 시스템에서 Redis 명령어별 hotkey 관리 여부를 분석하고, GET 요청을 hotkey로 관리해야 하는지 검토합니다.

## 현재 구조

### 1. INCRBY (쓰기) - Hotkey로 관리됨

**코드 위치:** `src/redis/fixed_cache_impl.go:198-223`

```go
// Hot key detection for INCRBY
if this.hotKeyDetector != nil && this.hotKeyBatcher != nil &&
   this.hotKeyDetector.RecordAccess(cacheKey.Key) {
    // Hot key: submit to batcher for 300us flush window
    logger.Debugf("hot key detected: %s", cacheKey.Key)
    hotKeyResultChans[i] = this.hotKeyBatcher.Submit(cacheKey.Key, hitsAddend, expirationSeconds)
} else {
    // Normal key: add to pipeline
    pipelineAppend(this.client, &pipeline, cacheKey.Key, hitsAddend, &results[i], expirationSeconds)
}
```

**동작 방식:**
- `RecordAccess()`로 접근 빈도 추적 (Count-Min Sketch 사용)
- Threshold(기본 100) 초과 시 hotkey로 분류
- Hotkey는 `HotKeyBatcher`로 300μs 단위 배치 처리
- INCRBY + EXPIRE를 하나의 pipeline으로 묶어 실행

### 2. GET (읽기) - Hotkey로 관리되지 않음

**코드 위치:** `src/redis/fixed_cache_impl.go:135-176`

```go
// stopCacheKeyIncrementWhenOverlimit 기능에서만 사용
if this.stopCacheKeyIncrementWhenOverlimit && !isCacheKeyOverlimit {
    for i, cacheKey := range cacheKeys {
        if cacheKey.Key == "" {
            continue
        }
        // 단순히 pipeline에 GET 추가
        pipelineAppendtoGet(this.client, &pipelineToGet, cacheKey.Key, &currentCount[i])
    }
    // Pipeline 실행
    checkError(this.client.PipeDo(pipelineToGet))
}
```

**동작 방식:**
- Hotkey 감지 없음
- 단순 pipeline 일괄 처리만 수행
- Near-limit 체크를 위한 현재 카운터 값 조회 목적

## GET 요청을 Hotkey로 관리해야 할까?

### ❌ 권장하지 않음

#### 1. **정확도 문제 (가장 중요)**

```go
// GET은 near-limit 판단을 위해 정확한 실시간 값이 필요
limitBeforeIncrease := currentCount[i]
limitAfterIncrease := limitBeforeIncrease + hitsAddends[i]
limitInfo := limiter.NewRateLimitInfo(limits[i], limitBeforeIncrease, limitAfterIncrease, 0, 0)

if this.baseRateLimiter.IsOverLimitThresholdReached(limitInfo) {
    nearlimitIndexes[i] = true  // 정확한 값 필요!
}
```

**비교:**
- **INCRBY 배치**: 누적 연산이므로 순서만 보장되면 최종 결과는 동일
  - 예: `INCRBY key 1` + `INCRBY key 2` = `INCRBY key 3` (순서 무관)
- **GET 배치/캐싱**: 시점에 따라 다른 값을 반환할 수 있음
  - 예: 캐시된 값(50) vs 실제 값(53) → near-limit 판단 오류 발생

#### 2. **사용 빈도가 낮음**

GET은 `stopCacheKeyIncrementWhenOverlimit=true`일 때만 실행됩니다:
- 기본값은 `false`
- 대부분의 배포 환경에서 비활성화 상태
- Redis 부하의 주원인이 아님

#### 3. **Hotkey 문제 패턴 불일치**

Hotkey 문제는 주로 **쓰기 집중**에서 발생합니다:
- **INCRBY**: 초당 수만 건의 쓰기 → Redis single-thread 병목
- **GET**: 읽기는 Redis가 효율적으로 처리 (읽기 최적화된 구조)

#### 4. **Local Cache로 이미 해결 가능**

현재 시스템에는 이미 local cache가 있습니다:

```go
// local cache에서 over-limit 체크
if this.baseRateLimiter.IsOverLimitWithLocalCache(cacheKey.Key) {
    isOverLimitWithLocalCache[i] = true
    overlimitIndexes[i] = true
}
```

- Local cache hit 시 GET 자체가 skip됨
- GET이 발생하는 경우는 이미 "정확한 값이 필요한 edge case"

### ✅ 예외적으로 고려할 수 있는 경우

만약 다음 조건을 **모두** 만족한다면 제한적으로 고려 가능:

1. **동일한 키에 대해 짧은 시간(< 1ms) 내에 여러 GET 요청이 발생**
   ```go
   // 같은 flush window 내 동일 키 GET 요청을 통합
   // 예: 100개 요청 → 1개 GET으로 결과 공유
   ```

2. **Eventual consistency 허용**
   - Near-limit 판단이 약간 부정확해도 괜찮은 경우
   - 예: 90% limit 경고를 89%로 판단해도 큰 문제 없음

3. **구현 복잡도 대비 효과가 명확**
   - 벤치마크로 GET 부하가 실제 병목임을 증명
   - INCRBY 대비 GET 비율이 매우 높은 경우

## 결론

**GET 요청은 hotkey로 관리하지 않는 것이 올바른 설계입니다.**

### 이유 요약:
1. **정확도**: Near-limit 판단에 실시간 값 필요
2. **설계 의도**: `stopCacheKeyIncrementWhenOverlimit`는 정확한 체크를 위한 기능
3. **성능**: GET은 Redis의 강점, 병목 아님
4. **복잡도**: 구현 비용 대비 효과 미미
5. **기존 해결책**: Local cache로 대부분 케이스 커버

### 대안:

GET 부하가 정말 문제라면:
- **Local cache hit ratio를 높이는 것이 더 효과적**
- `stopCacheKeyIncrementWhenOverlimit=false`로 설정하여 GET 자체를 제거
- Redis read replica 사용 (GET을 replica로 분산)

## Hotkey 감지 메커니즘

### Count-Min Sketch 알고리즘

**코드 위치:** `src/redis/hotkey_detector.go`

```go
// RecordAccess records an access to the key and returns whether the key is hot.
func (d *HotKeyDetector) RecordAccess(key string) bool {
    // Increment CMS counter
    count := d.cms.Increment(key, 1)

    // Fast path: check if already hot
    if d.isHot(key) {
        d.touchHotKey(key)
        return true
    }

    // Check if should become hot
    if count >= d.hotThreshold {
        d.promoteToHot(key)
        return true
    }

    return false
}
```

**주요 특징:**
- **Memory efficient**: 10MB로 수백만 키 추적
- **Probabilistic**: 약간의 오차 허용 (over-estimation)
- **Decay mechanism**: 10초마다 카운터 절반으로 감소 (시간에 따른 중요도 반영)
- **LRU eviction**: 최대 10,000개 hotkey 유지

### HotKeyBatcher 동작

**코드 위치:** `src/redis/hotkey_batcher.go`

```go
// Submit adds a key increment to the batch
func (b *HotKeyBatcher) Submit(key string, hitsAddend uint64, expirationSeconds int64) <-chan HotKeyBatcherResult {
    resultChan := make(chan HotKeyBatcherResult, 1)

    waiter := &pendingWaiter{
        hitsAddend: hitsAddend,
        resultChan: resultChan,
    }

    b.mu.Lock()
    agg, exists := b.pending[key]
    if !exists {
        agg = &aggregatedIncrement{
            expirationSeconds: expirationSeconds,
            waiters:           make([]*pendingWaiter, 0, 4),
        }
        b.pending[key] = agg
    }

    agg.totalHits += hitsAddend
    agg.waiters = append(agg.waiters, waiter)
    b.mu.Unlock()

    return resultChan
}
```

**배치 처리 예시:**

```
300μs 윈도우 내 동일 키 요청:
┌────────────────────────────────────────┐
│ Request 1: INCRBY key 1                │
│ Request 2: INCRBY key 2                │
│ Request 3: INCRBY key 1                │
│ Request 4: INCRBY key 3                │
└────────────────────────────────────────┘

Without batching:
  INCRBY key 1 → INCRBY key 1 → INCRBY key 1
  4개의 Redis 명령어

With batching (300μs 후):
  INCRBY key 7  (1+2+1+3 aggregated)
  1개의 Redis 명령어!

각 waiter는 자신의 증가 후 값을 받음:
  Request 1: 1
  Request 2: 3
  Request 3: 4
  Request 4: 7
```

## 관련 설정

### Hotkey 설정

```go
type HotKeyConfig struct {
    Enabled           bool          // Hotkey 기능 활성화
    SketchMemoryBytes int           // Count-Min Sketch 메모리 (기본: 10MB)
    SketchDepth       int           // Hash 함수 개수 (기본: 4)
    Threshold         uint32        // Hotkey 판단 임계값 (기본: 100)
    MaxHotKeys        int           // 최대 hotkey 개수 (기본: 10,000)
    FlushWindow       time.Duration // Batch flush 간격 (기본: 300μs)
    DecayInterval     time.Duration // Decay 주기 (기본: 10s)
}
```

### 권장 설정

```yaml
# 고부하 환경
HOTKEY_ENABLED: true
HOTKEY_THRESHOLD: 100              # 초당 100회 이상 접근 시 hotkey
HOTKEY_FLUSH_WINDOW: 300us         # 300마이크로초마다 flush
HOTKEY_MAX_KEYS: 10000             # 최대 10,000개 hotkey 추적

# 저부하 환경
HOTKEY_ENABLED: false              # 비활성화 (overhead 제거)
```

## 성능 영향

### Hotkey 배치 처리 효과

```
시나리오: 초당 100,000 요청, 상위 10개 키가 80% 차지

Without hotkey batching:
  Redis writes: 100,000/sec
  Redis CPU: 85%
  P99 latency: 15ms

With hotkey batching (300μs window):
  Redis writes: ~25,000/sec (75% 감소)
  Redis CPU: 30%
  P99 latency: 5ms

효과:
  - Redis write 75% 감소
  - CPU 사용률 55% 감소
  - Latency 66% 개선
```

## 참고 자료

- **Hotkey Detector 구현**: `src/redis/hotkey_detector.go`
- **Hotkey Batcher 구현**: `src/redis/hotkey_batcher.go`
- **Count-Min Sketch**: `src/redis/count_min_sketch.go`
- **Fixed Cache 구현**: `src/redis/fixed_cache_impl.go`

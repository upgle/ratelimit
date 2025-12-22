# stopCacheKeyIncrementWhenOverlimit 설정 가이드

## 개요

`stopCacheKeyIncrementWhenOverlimit`는 over-limit 또는 near-limit이 아닌 키의 Redis 카운터 증가를 중단하는 최적화 기능입니다. 이 문서는 이 설정의 동작 원리와 사용 시나리오를 설명합니다.

## 핵심 개념

**"Over-limit 또는 Near-limit이 아닌 키의 Redis 카운터 증가를 멈춤"**

### 일반적인 경우: INCRBY로 현재 값 확인

일반적으로는 INCRBY 자체가 증가 **후의 값**을 반환하므로 별도의 GET이 필요 없습니다.

```go
// fixed_cache_impl.go:257-258
limitAfterIncrease := results[i]  // INCRBY 결과 = 증가 후 값
limitBeforeIncrease := limitAfterIncrease - hitsAddends[i]  // 증가 전 값 계산
```

**일반 동작:**
```
1. INCRBY key 1 실행 → 결과: 51 반환
2. 증가 전 값: 51 - 1 = 50
3. Limit 체크: 50 → 51 (OK or OVER_LIMIT)
```

### 문제: INCRBY는 "무조건 증가"함

```go
// pipelineAppend (54번 라인)
*pipeline = client.PipeAppend(*pipeline, result, "INCRBY", key, hitsAddend)
```

**Redis INCRBY 특성:**
- `INCRBY key 0` → Redis write 발생 (값은 안 바뀌지만)
- `INCRBY key 1` → Redis write 발생 + 값 증가

**Over-limit 상태의 문제:**
```
DDoS 공격 시 over-limit 상태에서도:
INCRBY user:123:minute 1  → 101 (DENY)
INCRBY user:123:minute 1  → 102 (DENY)
INCRBY user:123:minute 1  → 103 (DENY)
...
INCRBY user:123:minute 1  → 10,000 (DENY)

문제: 모두 DENY될 요청인데 Redis write가 9,900번 발생!
```

## 동작 로직

### getHitsAddend 함수

**코드 위치:** `src/redis/fixed_cache_impl.go:62-90`

```go
func (this *fixedRateLimitCacheImpl) getHitsAddend(
    hitsAddend uint64,
    isCacheKeyOverlimit bool,
    isCacheKeyNearlimit bool,
    isNearLimit bool,
) uint64 {
    // false: 항상 증가
    if !this.stopCacheKeyIncrementWhenOverlimit {
        return hitsAddend
    }

    // true인 경우:

    // 1. Over-limit이면 증가 안함
    if isCacheKeyOverlimit {
        return 0  // ❌ Redis write 안함
    }

    // 2. 어떤 키도 near-limit 아니면 모두 증가
    if !isCacheKeyNearlimit {
        return hitsAddend  // ✅ 정상 증가
    }

    // 3. 일부 키가 near-limit인 경우
    if isNearLimit {
        return hitsAddend  // ✅ 이 키가 near-limit이면 증가
    } else {
        return 0  // ❌ near-limit 아니면 증가 안함
    }
}
```

### GET 수행 조건

**코드 위치:** `src/redis/fixed_cache_impl.go:135-176`

```go
// Local cache에 over-limit이 없을 때만 GET 수행
if this.stopCacheKeyIncrementWhenOverlimit && !isCacheKeyOverlimit {
    // 모든 키에 대해 GET 수행
    for i, cacheKey := range cacheKeys {
        pipelineAppendtoGet(this.client, &pipelineToGet, cacheKey.Key, &currentCount[i])
    }

    // GET 결과로 near-limit 판단
    for i, cacheKey := range cacheKeys {
        limitBeforeIncrease := currentCount[i]
        limitAfterIncrease := limitBeforeIncrease + hitsAddends[i]

        if this.baseRateLimiter.IsOverLimitThresholdReached(limitInfo) {
            nearlimitIndexes[i] = true
            isCacheKeyNearlimit = true
        }
    }
}
```

**GET이 나가는 조건:**
1. `stopCacheKeyIncrementWhenOverlimit = true` **AND**
2. Local cache에서 **아무 키도 over-limit이 아님**

→ **모든 키**에 대해 GET을 수행하여 near-limit 여부를 확인

## 동작 시나리오

### false (기본값): 항상 INCRBY

```
시나리오: 1분당 100개 제한, DDoS 공격으로 10,000 요청 유입

Redis 동작:
┌─────────────────────────────────────────────────┐
│ 요청 1-100:   INCRBY → 1,2,3,...,100           │ ✅ ALLOW
│ 요청 101:     INCRBY → 101                      │ ❌ DENY (over-limit)
│ 요청 102:     INCRBY → 102                      │ ❌ DENY
│ 요청 103:     INCRBY → 103                      │ ❌ DENY
│ ...                                             │
│ 요청 10,000:  INCRBY → 10,000                   │ ❌ DENY
└─────────────────────────────────────────────────┘

Redis Write: 10,000번 ← 9,900번은 불필요!
Redis 카운터 최종값: 10,000
CPU 사용: 높음 (의미없는 write 처리)

문제점:
1. Over-limit 상태에서도 계속 카운터 증가
2. 공격자가 Redis에 부하를 줄 수 있음
3. 정확한 통계가 어려움 (실제 사용: 100, 기록: 10,000)
```

### true: 조건부 INCRBY

```
시나리오: 동일 (1분당 100개 제한, 10,000 요청)

Redis 동작:
┌─────────────────────────────────────────────────┐
│ 요청 1-89:    GET → INCRBY (여유 있음)          │ ✅ ALLOW
│ 요청 90:      GET → 90, near-limit! → INCRBY   │ ✅ ALLOW
│ 요청 91-100:  GET → INCRBY (near-limit 추적)   │ ✅ ALLOW
│ 요청 101:     Local cache hit → Redis 안감!    │ ❌ DENY
│ 요청 102:     Local cache hit → Redis 안감!    │ ❌ DENY
│ ...                                             │
│ 요청 10,000:  Local cache hit → Redis 안감!    │ ❌ DENY
└─────────────────────────────────────────────────┘

Redis Write: ~200번 (GET 100 + INCRBY 100)
Redis 카운터 최종값: 100
CPU 사용: 낮음 (98% 감소!)

장점:
1. Over-limit 도달 후 Redis 부하 없음
2. DDoS 공격에 강함
3. 정확한 통계 (실제 허용된 요청만 기록)
```

## 세부 시나리오

### 시나리오 1: 정상 트래픽 (모두 Normal)

```
요청: user:A (10/100), user:B (20/100), user:C (30/100)
nearLimitRatio: 0.9 (90% 도달 시 near-limit)

stopCacheKeyIncrementWhenOverlimit = false:
├─ INCRBY user:A 1 → 11
├─ INCRBY user:B 1 → 21
└─ INCRBY user:C 1 → 31
Total: 3 writes

stopCacheKeyIncrementWhenOverlimit = true:
├─ GET user:A → 10 (10+1=11 < 90, NOT near)
├─ GET user:B → 20 (20+1=21 < 90, NOT near)
├─ GET user:C → 30 (30+1=31 < 90, NOT near)
├─ isCacheKeyNearlimit = false
├─ INCRBY user:A 1 → 11  ✅ 모두 normal이면 증가
├─ INCRBY user:B 1 → 21
└─ INCRBY user:C 1 → 31
Total: 3 GETs + 3 writes

⚠️ 결론: 정상 트래픽에서는 GET overhead 발생!
```

### 시나리오 2: Near-limit 혼재

```
요청: user:A (50/100), user:B (92/100 near!), user:C (30/100)

stopCacheKeyIncrementWhenOverlimit = false:
├─ INCRBY user:A 1 → 51
├─ INCRBY user:B 1 → 93
└─ INCRBY user:C 1 → 31
Total: 3 writes

stopCacheKeyIncrementWhenOverlimit = true:
├─ GET user:A → 50 (50+1=51 < 90, NOT near)
├─ GET user:B → 92 (92+1=93 >= 90, NEAR!) ⚠️
├─ GET user:C → 30 (30+1=31 < 90, NOT near)
├─ isCacheKeyNearlimit = true (B 때문)
├─ INCRBY user:B 1 → 93  ✅ Near-limit만 증가
├─ user:A skip (GET 결과 50 사용)
└─ user:C skip (GET 결과 30 사용)
Total: 3 GETs + 1 write

✅ 결론: Near-limit 키만 정밀 추적, 나머지는 Redis write 절약
```

### 시나리오 3: DDoS 공격 (Over-limit)

```
공격: user:attacker (105/100 이미 over), 1,000번 더 요청

stopCacheKeyIncrementWhenOverlimit = false:
├─ INCRBY user:attacker 1 → 106  ❌ DENY
├─ INCRBY user:attacker 1 → 107  ❌ DENY
├─ ... (998번 더)
└─ INCRBY user:attacker 1 → 1,105  ❌ DENY
Total: 1,000 writes (불필요!)
Redis 부하: 높음 ⚠️

stopCacheKeyIncrementWhenOverlimit = true:
├─ Local cache: "user:attacker over-limit"
├─ 요청 1: Local cache hit → Redis 안감  ❌ DENY
├─ 요청 2: Local cache hit → Redis 안감  ❌ DENY
├─ ... (998번 동일)
└─ 요청 1,000: Local cache hit → Redis 안감  ❌ DENY
Total: 0 writes!
Redis 부하: 없음 ✅

✅ 결론: 공격 트래픽을 Local cache에서 차단, Redis 보호
```

## 언제 true로 설정해야 하는가?

### ✅ true 권장 상황

#### 1. DDoS/공격 대비가 중요한 서비스

```
예시: Public API, 인증 없는 엔드포인트
- Rate limit을 초과하는 악의적 트래픽 예상
- Redis를 공격으로부터 보호 필요
- Over-limit 상태에서 Redis write 차단 필수

효과:
- Over-limit 이후 Redis write 0
- 공격자가 Redis에 부하를 줄 수 없음
- 서비스 안정성 향상

설정: stopCacheKeyIncrementWhenOverlimit = true
```

#### 2. Redis 부하가 높은 환경

```
예시: 초당 수십만 요청 처리
- Redis CPU 사용률 > 70%
- Write 병목 발생
- Over-limit/Normal 키의 불필요한 write 제거 필요

효과:
- Write 연산 50-90% 감소
- Redis CPU 사용률 감소
- P99 latency 개선

설정: stopCacheKeyIncrementWhenOverlimit = true
```

#### 3. 정확한 통계가 중요한 경우

```
예시: 과금, Quota 관리
- 실제 허용된 요청만 카운트 필요
- Over-limit 이후 카운터 증가 불필요
- 통계 정확성 > 성능

효과:
- Redis 카운터 = 실제 허용된 요청 수
- 정확한 과금/quota 계산
- 감사(audit) 용이

설정: stopCacheKeyIncrementWhenOverlimit = true
```

#### 4. 대부분의 키가 Limit에 근접하는 경우

```
예시: Quota 기반 서비스 (99% 사용자가 limit 근처까지 사용)
- Near-limit 비율 > 50%
- GET overhead < INCRBY 절약 효과

효과:
- Near-limit 키 정밀 추적
- Normal 키 Redis write 절약
- 전체적으로 write 감소

설정: stopCacheKeyIncrementWhenOverlimit = true
```

### ❌ false 권장 상황 (기본값)

#### 1. 정상 트래픽 위주의 서비스

```
예시: 내부 API, 인증된 사용자만 접근
- 대부분 요청이 limit 여유 있음 (< 50%)
- DDoS 공격 가능성 낮음
- GET overhead가 더 큼

효과:
- 불필요한 GET 제거
- 단순한 INCRBY만 사용
- Latency 최소화

설정: stopCacheKeyIncrementWhenOverlimit = false
```

#### 2. Redis 부하가 낮은 환경

```
예시: 트래픽이 적은 서비스, Redis 여유 있음
- Redis CPU < 30%
- Write 병목 없음
- 최적화 불필요

효과:
- 단순성 유지
- GET overhead 제거
- 코드 복잡도 감소

설정: stopCacheKeyIncrementWhenOverlimit = false
```

#### 3. 모든 요청의 정확한 카운트가 필요한 경우

```
예시: 분석, 로깅 목적
- Over-limit 이후에도 시도 횟수 기록 필요
- "차단된 요청" 통계 수집
- 공격 패턴 분석

효과:
- 모든 요청 카운트 (101, 102, 103...)
- 공격 규모 파악
- 상세한 분석 가능

설정: stopCacheKeyIncrementWhenOverlimit = false
```

#### 4. Local cache hit rate가 낮은 경우

```
예시: 분산 환경, 많은 인스턴스
- Local cache가 자주 miss
- Over-limit 판단을 Redis에서 해야 함
- Local cache 효과 미미

효과:
- 복잡도 감소
- GET overhead 회피
- 단순한 아키텍처

설정: stopCacheKeyIncrementWhenOverlimit = false
```

## 성능 비교

### 시나리오: 초당 10,000 요청, 1,000 RPS limit

#### 정상 트래픽 (800 RPS 사용)

```
┌──────────────────────────────────────────┐
│ false: INCRBY만                          │
│ - Redis: 800 writes/sec                  │
│ - Latency: 5ms                           │
├──────────────────────────────────────────┤
│ true: GET + INCRBY                       │
│ - Redis: 800 GETs + 800 writes/sec       │
│ - Latency: 7ms (+40%)                    │
└──────────────────────────────────────────┘

결론: false가 유리 ✅
```

#### 공격 시나리오 (15,000 RPS 유입)

```
┌──────────────────────────────────────────┐
│ false: INCRBY만                          │
│ - Redis: 15,000 writes/sec ⚠️            │
│ - CPU: 95% (병목!)                       │
│ - Latency: 50ms+ (degraded)             │
├──────────────────────────────────────────┤
│ true: Local cache 차단                   │
│ - Redis: 1,000 GETs + 1,000 writes/sec  │
│ - CPU: 15%                               │
│ - Latency: 6ms (정상)                    │
└──────────────────────────────────────────┘

결론: true가 필수 ✅
```

## 설정 가이드

### 환경 변수

```bash
# 기본 설정
STOP_CACHE_KEY_INCREMENT_WHEN_OVERLIMIT=false

# 공격 대비 필요 시
STOP_CACHE_KEY_INCREMENT_WHEN_OVERLIMIT=true
NEAR_LIMIT_RATIO=0.9  # 90% 도달 시 near-limit

# 최적화
LOCAL_CACHE_SIZE_IN_BYTES=1000000  # Local cache 크게 (중요!)
EXPIRATION_JITTER_MAX_SECONDS=5    # Jitter로 분산
```

### YAML 설정 예시

```yaml
# config.yaml

# 공격 대비 설정
runtime:
  stop_cache_key_increment_when_overlimit: true
  near_limit_ratio: 0.9
  local_cache_size_in_bytes: 10000000

# 정상 트래픽 설정
runtime:
  stop_cache_key_increment_when_overlimit: false
  local_cache_size_in_bytes: 1000000
```

### Go 코드 설정

```go
settings := settings.Settings{
    StopCacheKeyIncrementWhenOverlimit: true,
    NearLimitRatio:                     0.9,
    LocalCacheSizeInBytes:              10 * 1024 * 1024, // 10MB
}
```

## 의사결정 플로우차트

```
시작
 │
 ├─ DDoS 공격 대비 필요?
 │   ├─ YES → true ✅
 │   └─ NO
 │        │
 │        ├─ Redis CPU > 70%?
 │        │   ├─ YES → true ✅
 │        │   └─ NO
 │        │        │
 │        │        ├─ 정확한 통계 필요?
 │        │        │   ├─ YES (과금용) → true ✅
 │        │        │   ├─ YES (분석용) → false ✅
 │        │        │   └─ NO
 │        │        │        │
 │        │        │        ├─ Near-limit 비율 > 50%?
 │        │        │        │   ├─ YES → true ✅
 │        │        │        │   └─ NO → false ✅
```

## 의사결정 테이블

| 상황 | 설정 | 이유 |
|------|------|------|
| Public API, DDoS 위험 | **true** | Redis 보호, 공격 차단 |
| 내부 API, 정상 트래픽 | **false** | 단순성, GET overhead 제거 |
| Redis 부하 높음 (>70%) | **true** | Write 절약 |
| Redis 여유 있음 (<30%) | **false** | 최적화 불필요 |
| 과금/Quota 관리 | **true** | 통계 정확성 |
| 분석/로깅 목적 | **false** | 모든 시도 기록 |
| Near-limit 비율 > 50% | **true** | 효과 큼 |
| Near-limit 비율 < 10% | **false** | GET overhead만 증가 |
| Local cache hit rate > 80% | **true** | 효과적 |
| Local cache hit rate < 50% | **false** | 효과 미미 |

## 모니터링 메트릭

### 성능 메트릭

```
중요 지표:
1. redis_writes_per_second
   - true: 감소 예상
   - false: 높음

2. redis_cpu_usage
   - true: 낮음
   - false: 높을 수 있음

3. local_cache_hit_ratio
   - true: 높아야 효과적 (>80%)
   - false: 무관

4. p99_latency
   - true: GET overhead 있을 수 있음
   - false: 낮음 (정상 트래픽)
```

### 알림 설정

```yaml
alerts:
  - name: HighRedisWrites
    condition: redis_writes_per_second > 50000
    action: "Consider enabling stopCacheKeyIncrementWhenOverlimit"

  - name: LowLocalCacheHitRate
    condition: local_cache_hit_ratio < 0.5
    action: "Increase local cache size or disable stopCacheKeyIncrementWhenOverlimit"

  - name: HighGETOverhead
    condition: avg_get_latency > avg_incrby_latency * 2
    action: "Consider disabling stopCacheKeyIncrementWhenOverlimit"
```

## 주의사항

### 1. Local Cache 크기

**중요:** `stopCacheKeyIncrementWhenOverlimit=true`일 때 local cache가 핵심입니다!

```go
// 작은 local cache (비추천)
LocalCacheSizeInBytes: 100 * 1024  // 100KB
→ Over-limit 판단을 자주 miss
→ Redis로 GET 요청 증가
→ 효과 감소

// 충분한 local cache (권장)
LocalCacheSizeInBytes: 10 * 1024 * 1024  // 10MB
→ Over-limit 판단 hit rate 높음
→ Redis 부하 감소
→ 효과 극대화
```

### 2. Near Limit Ratio 조정

```go
// 보수적 (더 일찍 near-limit 판단)
NearLimitRatio: 0.8  // 80% 도달 시
→ 더 많은 키가 near-limit
→ GET 빈도 증가
→ 정밀한 추적

// 공격적 (늦게 near-limit 판단)
NearLimitRatio: 0.95  // 95% 도달 시
→ 적은 키만 near-limit
→ GET 빈도 감소
→ Redis write 절약 극대화
```

### 3. 분산 환경 고려

```
다수의 인스턴스 (예: 100개):
- Local cache는 인스턴스별로 독립적
- Over-limit 판단이 인스턴스마다 다를 수 있음
- 일부 불일치 허용 필요

권장:
- Local cache 크기를 충분히
- TTL을 짧게 (빠른 동기화)
- 약간의 over-provisioning 허용
```

## 트러블슈팅

### 문제 1: GET overhead가 큼

```
증상:
- Latency 증가
- Redis read 부하 높음

원인:
- Near-limit 키가 너무 많음
- 모든 요청에 GET 발생

해결:
1. NearLimitRatio 증가 (0.9 → 0.95)
2. 또는 false로 변경
```

### 문제 2: Over-limit 차단이 안됨

```
증상:
- 공격 시 Redis write 여전히 높음
- CPU 사용률 높음

원인:
- Local cache가 작음
- Local cache hit rate 낮음

해결:
1. Local cache 크기 증가
2. Local cache TTL 조정
```

### 문제 3: 통계 불일치

```
증상:
- Redis 카운터 < 실제 요청 수
- 일부 요청이 기록 안됨

원인:
- Normal 키는 INCRBY skip됨
- 의도된 동작

해결:
1. 통계 목적이면 false 사용
2. 또는 별도 로깅 추가
```

## 결론

### 요약

- **일반적으로는 false** (기본값, 단순함)
- **공격 대비나 Redis 보호가 중요하면 true**
- **Local cache hit rate가 높아야 효과적**

### 핵심 체크리스트

```
✅ true로 설정하기 전 확인:
- [ ] DDoS 공격 대비가 필요한가?
- [ ] Redis CPU 사용률이 높은가? (>70%)
- [ ] Local cache 크기가 충분한가? (>10MB)
- [ ] Local cache hit rate를 모니터링할 수 있는가?
- [ ] Near-limit 비율이 높은가? (>50%)

✅ false로 유지할 조건:
- [ ] 정상 트래픽 위주인가?
- [ ] Redis 부하가 낮은가? (<30%)
- [ ] 모든 요청 카운트가 필요한가?
- [ ] 단순성을 선호하는가?
```

## 참고 자료

- **구현 코드**: `src/redis/fixed_cache_impl.go`
- **설정 파일**: `src/settings/settings.go`
- **Hotkey 분석**: `docs/hotkey-analysis.md`
- **Local Cache**: `github.com/coocood/freecache`

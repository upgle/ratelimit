# Ratelimit 기술 문서

이 디렉토리는 ratelimit 시스템의 주요 기능과 최적화 기법에 대한 심층 분석 문서를 포함합니다.

## 문서 목록

### 1. [Hotkey 관리 분석](hotkey-analysis.md)

Redis hotkey 감지 및 배치 처리 메커니즘에 대한 분석입니다.

**주요 내용:**
- INCRBY vs GET 명령어의 hotkey 관리 차이
- GET 요청을 hotkey로 관리하지 않는 이유
- Count-Min Sketch 알고리즘
- HotKeyBatcher의 동작 원리
- 성능 최적화 효과

**다루는 질문:**
- 왜 GET 요청은 hotkey로 관리하지 않는가?
- Hotkey 배치 처리가 어떻게 성능을 개선하는가?
- Near-limit 판단에 정확한 값이 왜 중요한가?

### 2. [stopCacheKeyIncrementWhenOverlimit 설정 가이드](stop-cache-key-increment-guide.md)

Over-limit 키의 Redis 카운터 증가를 중단하는 최적화 기능에 대한 완벽 가이드입니다.

**주요 내용:**
- INCRBY의 특성과 문제점
- GET을 통한 사전 체크 메커니즘
- true/false 설정의 동작 차이
- 실제 시나리오별 성능 비교
- 의사결정 가이드 및 트러블슈팅

**다루는 질문:**
- stopCacheKeyIncrementWhenOverlimit가 필요한 이유는?
- 언제 true로 설정해야 하는가?
- GET은 언제 수행되는가?
- DDoS 공격 시 어떻게 Redis를 보호하는가?

## 빠른 참조

### Hotkey 관련 설정

```yaml
# Hotkey 기능 활성화
HOTKEY_ENABLED: true
HOTKEY_THRESHOLD: 100              # 초당 100회 이상 접근 시 hotkey
HOTKEY_FLUSH_WINDOW: 300us         # 300마이크로초마다 flush
HOTKEY_MAX_KEYS: 10000             # 최대 10,000개 hotkey 추적
```

### stopCacheKeyIncrementWhenOverlimit 설정

```yaml
# DDoS 대비 (권장)
STOP_CACHE_KEY_INCREMENT_WHEN_OVERLIMIT: true
NEAR_LIMIT_RATIO: 0.9
LOCAL_CACHE_SIZE_IN_BYTES: 10000000

# 정상 트래픽 (기본값)
STOP_CACHE_KEY_INCREMENT_WHEN_OVERLIMIT: false
LOCAL_CACHE_SIZE_IN_BYTES: 1000000
```

## 의사결정 플로우

### Hotkey 기능 활성화 여부

```
초당 요청 수 > 10,000?
├─ YES → Hotkey 활성화 ✅
└─ NO
     │
     └─ 상위 10% 키가 전체 트래픽의 80% 이상?
         ├─ YES → Hotkey 활성화 ✅
         └─ NO → Hotkey 비활성화
```

### stopCacheKeyIncrementWhenOverlimit 설정

```
DDoS 공격 대비 필요?
├─ YES → true ✅
└─ NO
     │
     └─ Redis CPU > 70%?
         ├─ YES → true ✅
         └─ NO → false ✅
```

## 성능 영향 요약

### Hotkey 배치 처리

```
효과: Redis write 75% 감소, CPU 55% 감소, Latency 66% 개선
조건: 상위 10개 키가 전체 트래픽의 80% 차지
```

### stopCacheKeyIncrementWhenOverlimit

```
효과 (공격 시): Redis write 98% 감소, CPU 안정화
효과 (정상): GET overhead로 latency 약간 증가 가능
조건: Local cache hit rate > 80%
```

## 관련 코드

| 기능 | 파일 경로 |
|------|----------|
| Hotkey Detector | `src/redis/hotkey_detector.go` |
| Hotkey Batcher | `src/redis/hotkey_batcher.go` |
| Count-Min Sketch | `src/redis/count_min_sketch.go` |
| Fixed Cache 구현 | `src/redis/fixed_cache_impl.go` |
| 설정 | `src/settings/settings.go` |

## 추가 리소스

### 벤치마크

- **성능 벤치마크**: `test/redis/bench_test.go`
  - 최대 성능 테스트 (BenchmarkParallelDoLimit)
  - 고정 요청률 테스트 (BenchmarkConstantRateDoLimit)
  - Redis CPU 모니터링 포함

### Docker 환경

- **Redis Cluster 설정**: `docker-compose-cluster.yml`
- **Grafana 모니터링**: `monitoring/`
- **설정 가이드**: `REDIS-CLUSTER-SETUP.md`

## 기여

문서 개선이나 추가 분석이 필요한 경우:
1. 이슈 생성
2. 분석 내용 작성
3. Pull request 제출

## 버전 정보

- **작성일**: 2025-12-21
- **분석 대상**: Ratelimit main branch
- **주요 파일**: `src/redis/fixed_cache_impl.go`, `src/redis/hotkey_batcher.go`

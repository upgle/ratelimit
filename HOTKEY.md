# Hot Key Detection 및 300μs Flush Window

## 개요

Redis Rate Limiter에서 빈번하게 접근되는 "Hot Key"를 감지하고, 해당 키에 대한 요청을 배치 처리하여 Redis 부하를 줄이는 기능입니다.

### 핵심 아이디어

```
일반 키:  요청 → 즉시 Redis INCRBY → 응답
Hot 키:   요청 → 배치 수집 (300μs) → 합쳐서 Redis INCRBY → 각 요청에 개별 응답
```

---

## 아키텍처

```
┌─────────────────────────────────────────────────────────────────┐
│                        DoLimit() 요청                           │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                    HotKeyDetector                               │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │              Count-Min Sketch (10MB)                     │   │
│  │  ┌─────────────────────────────────────────────────┐    │   │
│  │  │ depth=4, width=655,360                          │    │   │
│  │  │ 키 접근마다 카운터 증가                           │    │   │
│  │  │ 추정 빈도 = min(counter[0], ..., counter[3])    │    │   │
│  │  └─────────────────────────────────────────────────┘    │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
│  빈도 >= 임계값(100)?                                           │
│     │                                                           │
│     ├── No ──→ 일반 Pipeline으로 즉시 처리                      │
│     │                                                           │
│     └── Yes ─→ Hot Key로 등록 (LRU, 최대 10,000개)              │
└─────────────────────────────────────────────────────────────────┘
                                │
                          Hot Key인 경우
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                     HotKeyBatcher                               │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │ pending map[string]*aggregatedIncrement                  │   │
│  │                                                          │   │
│  │ key1: { totalHits: 15, waiters: [w1, w2, w3] }          │   │
│  │ key2: { totalHits: 8,  waiters: [w4, w5] }              │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
│  300μs 타이머 ────────────────────────────────────────────────→ │
│                                                                 │
│  flush():                                                       │
│    1. INCRBY key1 15                                           │
│    2. INCRBY key2 8                                            │
│    3. 각 waiter에게 개별 카운트 반환                             │
└─────────────────────────────────────────────────────────────────┘
```

---

## 구성 요소

### 1. Count-Min Sketch (CMS)

확률적 자료구조로 키의 접근 빈도를 추정합니다.

**파일:** `src/redis/countmin_sketch.go`

```go
type CountMinSketch struct {
    width    uint32       // 열 수 (메모리 크기에서 계산)
    depth    uint32       // 행 수 (해시 함수 개수)
    counters [][]uint32   // 2D 카운터 배열
    seeds    []uint64     // 각 행의 해시 시드
}
```

**동작 원리:**

```
키 "user:123" 접근 시:

  hash₀("user:123") → index 42    → counters[0][42]++
  hash₁("user:123") → index 1337  → counters[1][1337]++
  hash₂("user:123") → index 999   → counters[2][999]++
  hash₃("user:123") → index 5678  → counters[3][5678]++

추정 빈도 = min(counters[0][42], counters[1][1337],
                counters[2][999], counters[3][5678])
```

**메모리 계산:**
- 10MB 메모리, depth=4 → width = 10MB / (4 × 4bytes) = 655,360
- 오차율: 약 0.15%

**Decay (감쇠):**
- 10초마다 모든 카운터를 50%로 감소
- 트래픽 패턴 변화에 적응

### 2. Hot Key Detector

Hot Key를 감지하고 관리합니다.

**파일:** `src/redis/hotkey_detector.go`

```go
type HotKeyDetector struct {
    cms           *CountMinSketch
    hotThreshold  uint32              // Hot 판단 임계값 (기본: 100)
    hotKeys       map[string]struct{} // 현재 Hot Key 집합
    hotKeysList   []string            // LRU 순서 리스트
    maxHotKeys    int                 // 최대 Hot Key 개수
}
```

**Hot Key 판단 로직:**

```go
func (d *HotKeyDetector) RecordAccess(key string) bool {
    // 1. CMS에 접근 기록
    count := d.cms.Increment(key, 1)

    // 2. 이미 Hot Key인지 확인 (빠른 경로)
    if d.isHot(key) {
        d.touchHotKey(key)  // LRU 갱신
        return true
    }

    // 3. 임계값 도달 시 Hot Key로 승격
    if count >= d.hotThreshold {
        d.promoteToHot(key)
        return true
    }

    return false
}
```

**LRU Eviction:**
- Hot Key 개수가 maxHotKeys 초과 시
- 가장 오래 접근되지 않은 키를 제거

### 3. Hot Key Batcher

Hot Key 요청을 배치 처리합니다.

**파일:** `src/redis/hotkey_batcher.go`

```go
type HotKeyBatcher struct {
    client      Client
    flushWindow time.Duration          // 300μs
    pending     map[string]*aggregatedIncrement
}

type aggregatedIncrement struct {
    totalHits         uint64           // 합산된 증가량
    expirationSeconds int64            // 최대 TTL
    waiters           []*pendingWaiter // 대기 중인 요청들
}
```

**배치 처리 흐름:**

```
시간 0μs:    요청 A (key1, hits=1) 도착 → pending에 추가
시간 50μs:   요청 B (key1, hits=2) 도착 → pending에 누적
시간 150μs:  요청 C (key1, hits=1) 도착 → pending에 누적
시간 300μs:  flush() 트리거
             │
             ▼
         pending[key1] = {totalHits: 4, waiters: [A, B, C]}
             │
             ▼
         Redis INCRBY key1 4 → 결과: 104
             │
             ▼
         각 waiter에게 개별 결과 반환:
           - A: 101 (이전값 100 + 1)
           - B: 103 (101 + 2)
           - C: 104 (103 + 1)
```

**개별 결과 계산:**

```go
// 최종 결과에서 역순으로 계산
finalCount := 104
runningCount := finalCount

waiterResults[2] = 104  // C
runningCount -= 1       // → 103

waiterResults[1] = 103  // B
runningCount -= 2       // → 101

waiterResults[0] = 101  // A
```

---

## 설정 옵션

| 환경변수 | 기본값 | 설명 |
|---------|--------|------|
| `HOT_KEY_DETECTION_ENABLED` | `false` | Hot Key Detection 활성화 |
| `HOT_KEY_SKETCH_MEMORY_BYTES` | `10485760` (10MB) | CMS 메모리 크기 |
| `HOT_KEY_SKETCH_DEPTH` | `4` | CMS 해시 함수 개수 |
| `HOT_KEY_THRESHOLD` | `100` | Hot Key 판단 빈도 임계값 |
| `HOT_KEY_MAX_COUNT` | `10000` | 최대 Hot Key 개수 |
| `HOT_KEY_FLUSH_WINDOW` | `10us` | 배치 플러시 주기 |
| `HOT_KEY_DECAY_INTERVAL` | `10s` | CMS 카운터 감쇠 주기 |

---

## 전체 동작 흐름

```
                    Rate Limit 요청 도착
                           │
                           ▼
              ┌────────────────────────┐
              │  Cache Key 생성        │
              └────────────────────────┘
                           │
                           ▼
              ┌────────────────────────┐
              │  Local Cache 확인      │
              │  (Over Limit 체크)     │
              └────────────────────────┘
                           │
                           ▼
         ┌─────────────────────────────────────┐
         │  HotKeyDetector.RecordAccess(key)   │
         │                                     │
         │  CMS 카운터 증가                     │
         │  빈도 >= 임계값?                     │
         └─────────────────────────────────────┘
                    │              │
               Hot Key          일반 Key
                    │              │
                    ▼              ▼
    ┌──────────────────────┐  ┌──────────────────────┐
    │  HotKeyBatcher       │  │  즉시 Pipeline       │
    │  .Submit()           │  │  실행                │
    │                      │  │                      │
    │  pending에 추가       │  │  INCRBY key hits    │
    │  resultChan 대기     │  │  EXPIRE key ttl     │
    └──────────────────────┘  └──────────────────────┘
              │                        │
              ▼                        │
    ┌──────────────────────┐          │
    │  300μs 후 flush()    │          │
    │                      │          │
    │  합산 INCRBY 실행    │          │
    │  개별 결과 분배       │          │
    └──────────────────────┘          │
              │                        │
              └────────────┬───────────┘
                           │
                           ▼
              ┌────────────────────────┐
              │  결과 처리             │
              │                        │
              │  limitAfterIncrease    │
              │  = 반환받은 카운트      │
              │                        │
              │  remaining             │
              │  = limit - count       │
              └────────────────────────┘
                           │
                           ▼
              ┌────────────────────────┐
              │  응답 반환             │
              │  OK / OVER_LIMIT       │
              └────────────────────────┘
```

---

## Rate Limit 정확성

### 문제: 배치 처리 시 정확한 remaining 계산

여러 요청이 배치로 처리될 때, 각 요청이 정확한 `remaining` 값을 받아야 합니다.

**해결책:** 각 요청에 순차적인 개별 카운트 반환

```
Limit: 100, 현재 카운트: 97

요청 1 (hits=1): 98 반환 → remaining = 2, OK
요청 2 (hits=1): 99 반환 → remaining = 1, OK
요청 3 (hits=1): 100 반환 → remaining = 0, OK
요청 4 (hits=1): 101 반환 → remaining = -1, OVER_LIMIT
```

각 요청이 자신의 정확한 위치를 알 수 있어 rate limit 판단이 올바르게 이루어집니다.

---

## 성능 특성

### Redis 요청 감소

```
Hot Key 없이:   N개 요청 → N번 Redis 호출
Hot Key 사용:   N개 요청 → 1번 Redis 호출 (300μs 윈도우 내)
```

### 지연 시간

- **일반 키:** 즉시 처리 (기존과 동일)
- **Hot 키:** 최대 300μs 추가 지연 (배치 윈도우)

### 메모리 사용량

| 구성 요소 | 메모리 |
|----------|--------|
| Count-Min Sketch | 10MB (설정 가능) |
| Hot Key 집합 | ~160KB (10,000개 키 기준) |
| Batcher pending | 요청에 비례 |

---

## 파일 구조

```
src/redis/
├── countmin_sketch.go    # Count-Min Sketch 구현
├── hotkey_detector.go    # Hot Key 감지 및 관리
├── hotkey_batcher.go     # 300μs 배치 처리
├── fixed_cache_impl.go   # 통합 (DoLimit에서 사용)
└── cache_impl.go         # 설정 전달

src/settings/
└── settings.go           # 환경변수 설정
```

---

## 사용 예시

```bash
# 기본 활성화
export HOT_KEY_DETECTION_ENABLED=true

# 더 공격적인 Hot Key 감지 (낮은 임계값)
export HOT_KEY_THRESHOLD=50

# 더 큰 메모리로 정확도 향상
export HOT_KEY_SKETCH_MEMORY_BYTES=52428800  # 50MB

# 더 짧은 배치 윈도우 (지연 감소, Redis 호출 증가)
export HOT_KEY_FLUSH_WINDOW=100us
```

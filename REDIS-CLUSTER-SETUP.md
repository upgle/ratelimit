# Redis Cluster with Monitoring Setup

Redis Cluster 환경을 Docker로 구축하고 Grafana로 모니터링하는 가이드입니다.

## 아키텍처

### Redis Cluster
- **3 Master 노드**: 7001, 7002, 7003 포트
- 데이터 샤딩 (16384 slots)
- 고가용성 테스트 및 개발 환경용

### 모니터링 스택
- **Redis Exporter**: 각 Redis 노드의 메트릭 수집
- **Prometheus**: 메트릭 저장 및 쿼리
- **Grafana**: 시각화 대시보드

## 빠른 시작

### 1. 클러스터 시작

```bash
# 간단한 방법 (추천)
./scripts/start-cluster.sh

# 또는 직접 docker-compose 사용
docker compose -f docker-compose-perf.yml up -d redis-master-1 redis-master-2 redis-master-3 \
  redis-cluster-init redis-cluster-exporter-1 redis-cluster-exporter-2 redis-cluster-exporter-3 \
  prometheus grafana
```

### 2. 클러스터 상태 확인

```bash
./scripts/cluster-status.sh
```

### 3. 클러스터 테스트

```bash
# 데이터 쓰기
docker exec redis-master-1 redis-cli -c -p 7001 SET mykey "Hello Redis Cluster"

# 데이터 읽기
docker exec redis-master-1 redis-cli -c -p 7001 GET mykey

# 여러 키 테스트 (샤딩 확인)
for i in {1..10}; do
  docker exec redis-master-1 redis-cli -c -p 7001 SET key$i "value$i"
done

# 클러스터 정보 확인
docker exec redis-master-1 redis-cli -p 7001 cluster info
docker exec redis-master-1 redis-cli -p 7001 cluster nodes
```

## 서비스 접속 정보

| 서비스 | URL | 계정 |
|--------|-----|------|
| Grafana | http://localhost:3000 | admin / admin |
| Prometheus | http://localhost:9090 | - |
| Redis Master 1 | localhost:7001 | - |
| Redis Master 2 | localhost:7002 | - |
| Redis Master 3 | localhost:7003 | - |
| Redis Cluster Exporter 1 | http://localhost:9122 | - |
| Redis Cluster Exporter 2 | http://localhost:9123 | - |
| Redis Cluster Exporter 3 | http://localhost:9124 | - |

## Grafana 대시보드

1. Grafana 접속: http://localhost:3000
2. 로그인: admin / admin
3. 대시보드: "Redis Cluster Monitoring"

### 모니터링 메트릭

- **Commands Per Second**: 초당 처리 명령 수
- **CPU Usage**: Redis 프로세스 CPU 사용률
- **Memory Usage**: 메모리 사용량
- **Connected Clients**: 연결된 클라이언트 수
- **Total Keys**: 저장된 키 개수
- **Network I/O**: 네트워크 입출력

## 벤치마크 실행

### 최대 성능 테스트

```bash
cd test/redis
go test -bench=BenchmarkParallelDoLimit -benchtime=10s -benchmem
```

### 고정 요청률 테스트

```bash
# 1000 RPS로 10초간 테스트
go test -bench=BenchmarkConstantRateDoLimit/10k_rps -benchtime=10s

# 특정 요청 수만큼 테스트 (예: 5000 요청)
go test -bench=BenchmarkConstantRateDoLimit/10k_rps -benchtime=5000x
```

### Ratelimit 서비스와 함께 사용

성능 테스트는 `scripts/run-perf-test.sh`를 사용하세요:

```bash
# Redis Cluster 성능 테스트
./scripts/run-perf-test.sh --start-backends -e test/perf/endpoints.yaml -m

# 또는 수동으로 ratelimit 실행
REDIS_TYPE=cluster \
REDIS_URL=localhost:7001,localhost:7002,localhost:7003 \
./bin/ratelimit
```

## 클러스터 관리

### 클러스터 상태 확인

```bash
# 클러스터 정보
docker exec redis-master-1 redis-cli -p 7001 cluster info

# 노드 정보
docker exec redis-master-1 redis-cli -p 7001 cluster nodes

# 슬롯 분배 확인
docker exec redis-master-1 redis-cli -p 7001 cluster slots
```

### 특정 노드 연결

```bash
# Master 1 연결
docker exec -it redis-master-1 redis-cli -c -p 7001

# Master 2 연결
docker exec -it redis-master-2 redis-cli -c -p 7002
```

### 노드 장애 테스트

```bash
# Master 1 중지
docker stop redis-master-1

# 클러스터 상태 확인
docker exec redis-master-2 redis-cli -p 7002 cluster nodes

# Master 1 재시작
docker start redis-master-1

# 다시 클러스터 상태 확인
docker exec redis-master-2 redis-cli -p 7002 cluster nodes
```

## 메모리 및 성능 튜닝

docker-compose-perf.yml에서 각 Redis 노드의 설정을 수정할 수 있습니다:

```yaml
redis-master-1:
  command: >
    redis-server
    --port 7001
    --cluster-enabled yes
    --cluster-config-file nodes.conf
    --cluster-node-timeout 5000
    --appendonly yes
    --maxmemory 512mb                    # 최대 메모리 설정
    --maxmemory-policy allkeys-lru       # 메모리 정책
    --tcp-backlog 511                    # TCP backlog
    --timeout 0                          # 클라이언트 타임아웃
    --tcp-keepalive 300                  # TCP keepalive
```

## 클러스터 정리

```bash
# 클러스터 중지 (추천)
./scripts/stop-cluster.sh

# 또는 docker-compose 직접 사용
docker compose -f docker-compose-perf.yml down

# 모든 데이터 삭제 (주의!)
docker compose -f docker-compose-perf.yml down -v
```

## 트러블슈팅

### 클러스터 초기화 실패

```bash
# 모든 컨테이너와 볼륨 삭제 후 재시작
./scripts/stop-cluster.sh
docker compose -f docker-compose-perf.yml down -v
./scripts/start-cluster.sh
```

### Grafana 대시보드가 보이지 않는 경우

```bash
# Grafana 컨테이너 재시작
docker restart grafana

# 로그 확인
docker logs grafana
```

### Prometheus가 메트릭을 수집하지 못하는 경우

```bash
# Prometheus 설정 확인
docker exec prometheus cat /etc/prometheus/prometheus.yml

# Targets 확인
# http://localhost:9090/targets
```

## 프로덕션 고려사항

### 보안

1. **Redis 인증 추가**:
   ```yaml
   command: >
     redis-server
     --requirepass your-strong-password
   ```

2. **네트워크 격리**:
   - 프로덕션에서는 외부 포트 노출 최소화
   - 방화벽 규칙 설정

### 백업

```bash
# RDB 백업 트리거
docker exec redis-master-1 redis-cli -p 7001 BGSAVE

# AOF 파일은 자동으로 저장됨 (--appendonly yes)
```

### 리소스 제한

docker-compose-perf.yml에 이미 리소스 제한이 설정되어 있습니다:

```yaml
redis-master-1:
  deploy:
    resources:
      limits:
        cpus: '1'
        memory: 256M
```

## 참고 자료

- [Redis Cluster Tutorial](https://redis.io/docs/management/scaling/)
- [Redis Cluster Specification](https://redis.io/docs/reference/cluster-spec/)
- [Prometheus Redis Exporter](https://github.com/oliver006/redis_exporter)
- [Grafana Documentation](https://grafana.com/docs/)

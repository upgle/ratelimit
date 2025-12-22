#!/bin/bash

set -e

echo "=== Redis Cluster Status ==="
echo ""

echo "Cluster Info:"
docker exec redis-master-1 redis-cli -p 7001 cluster info || echo "Cluster not ready yet"

echo ""
echo "Cluster Nodes:"
docker exec redis-master-1 redis-cli -p 7001 cluster nodes || echo "Cluster not ready yet"

echo ""
echo "=== Container Status ==="
docker compose -f docker-compose-perf.yml ps 2>/dev/null || \
docker-compose -f docker-compose-perf.yml ps 2>/dev/null

echo ""
echo "=== Redis Memory Usage ==="
ports=(7001 7002 7003)
for i in {1..3}; do
    echo "Master $i (port ${ports[$i-1]}):"
    docker exec redis-master-$i redis-cli -p ${ports[$i-1]} INFO MEMORY | grep used_memory_human || true
done

echo ""
echo "=== Connected Clients ==="
for i in {1..3}; do
    echo "Master $i (port ${ports[$i-1]}):"
    docker exec redis-master-$i redis-cli -p ${ports[$i-1]} INFO CLIENTS | grep connected_clients || true
done

echo ""
echo "=== Service URLs ==="
echo "Prometheus: http://localhost:9090"
echo "Grafana: http://localhost:3000 (admin/admin)"
echo "Redis Cluster Exporters: http://localhost:9122-9124"
echo ""

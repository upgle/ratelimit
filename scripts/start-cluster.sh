#!/bin/bash

set -e

echo "Starting Redis Cluster with Monitoring..."

# Start all services
docker compose -f docker-compose-perf.yml up -d redis-master-1 redis-master-2 redis-master-3 redis-cluster-init \
  redis-cluster-exporter-1 redis-cluster-exporter-2 redis-cluster-exporter-3 prometheus grafana 2>/dev/null || \
docker-compose -f docker-compose-perf.yml up -d redis-master-1 redis-master-2 redis-master-3 redis-cluster-init \
  redis-cluster-exporter-1 redis-cluster-exporter-2 redis-cluster-exporter-3 prometheus grafana 2>/dev/null

echo "Waiting for Redis Cluster to initialize..."
sleep 15

# Check cluster status
echo ""
echo "=== Checking Redis Cluster Status ==="
docker exec redis-master-1 redis-cli -p 7001 cluster info

echo ""
echo "=== Redis Cluster Nodes ==="
docker exec redis-master-1 redis-cli -p 7001 cluster nodes

echo ""
echo "=== Services Status ==="
docker compose -f docker-compose-perf.yml ps redis-master-1 redis-master-2 redis-master-3 \
  redis-cluster-exporter-1 redis-cluster-exporter-2 redis-cluster-exporter-3 prometheus grafana 2>/dev/null || \
docker-compose -f docker-compose-perf.yml ps redis-master-1 redis-master-2 redis-master-3 \
  redis-cluster-exporter-1 redis-cluster-exporter-2 redis-cluster-exporter-3 prometheus grafana 2>/dev/null

echo ""
echo "========================================="
echo "Redis Cluster is ready!"
echo "========================================="
echo ""
echo "Services:"
echo "  - Redis Cluster Masters: localhost:7001-7003"
echo "  - Prometheus: http://localhost:9090"
echo "  - Grafana: http://localhost:3000 (admin/admin)"
echo ""
echo "Grafana Dashboards:"
echo "  - Redis Cluster Monitoring: http://localhost:3000/d/redis-cluster"
echo ""
echo "To test the cluster:"
echo "  docker exec redis-master-1 redis-cli -p 7001 -c SET mykey 'Hello Redis Cluster'"
echo "  docker exec redis-master-1 redis-cli -p 7001 -c GET mykey"
echo ""
echo "To stop the cluster:"
echo "  ./scripts/stop-cluster.sh"
echo ""

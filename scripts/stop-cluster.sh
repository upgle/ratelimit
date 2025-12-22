#!/bin/bash

set -e

echo "Stopping Redis Cluster and Monitoring stack..."

docker compose -f docker-compose-perf.yml down 2>/dev/null || \
docker-compose -f docker-compose-perf.yml down 2>/dev/null

echo ""
echo "Redis Cluster stopped successfully!"
echo ""
echo "To remove all data volumes, run:"
echo "  docker compose -f docker-compose-perf.yml down -v"
echo ""

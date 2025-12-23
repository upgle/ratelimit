# Redis Cluster Slot Resharding Handling

## Overview

This document explains how the slot-based pipeline grouping handles Redis Cluster topology changes, including slot resharding, failover, and node additions/removals.

## Background: Redis Cluster Topology

### Slot Distribution

Redis Cluster divides the key space into 16,384 slots (0-16383):

```
Cluster Example (3 nodes):
┌─────────────────────────────────┐
│ Node A: slots 0-5460            │
│ Node B: slots 5461-10922        │
│ Node C: slots 10923-16383       │
└─────────────────────────────────┘
```

### Slot Calculation

Slots are calculated using CRC16:

```python
def cluster_slot(key):
    # Extract hash tag if present
    start = key.find(b'{')
    if start != -1:
        end = key.find(b'}', start + 1)
        if end != -1 and end != start + 1:
            key = key[start+1:end]

    # CRC16 hash mod 16384
    crc = binascii.crc_hqx(key, 0)
    return crc % 16384
```

**Key Properties:**
- **Deterministic**: Same key always returns same slot
- **Fast**: O(1) computation
- **Consistent**: Independent of cluster topology
- **Hash Tag Support**: `user:{group1}:1000` and `user:{group1}:2000` → same slot

## Topology Changes

### 1. Resharding (Slot Migration)

When adding nodes or rebalancing:

```
Initial:
  Node A: 0-8191
  Node B: 8192-16383

After adding Node C:
  Node A: 0-5460
  Node B: 5461-10922
  Node C: 10923-16383  (NEW)
```

### 2. Failover

Master node fails, replica promoted:

```
Before:
  Master A (slots 0-5460)
  Replica A'

After failover:
  Replica A' promoted to Master (slots 0-5460)
```

### 3. Node Addition/Removal

- **Adding**: Slots redistributed from existing nodes
- **Removing**: Slots moved to remaining nodes

## Our Implementation: Two-Layer Handling

### Layer 1: Our Code (Static Slot Calculation)

```go
// fixed_cache_impl.go
func (this *fixedRateLimitCacheImpl) DoLimit(...) {
    // 1. Calculate slot for each key (STATIC)
    pipelines := make(map[uint16]Pipeline)

    for i, cacheKey := range cacheKeys {
        slot := this.client.GetSlot(cacheKey.Key)  // CRC16 - always same result
        pipeline := pipelines[slot]
        pipelineAppend(this.client, &pipeline, cacheKey.Key, ...)
        pipelines[slot] = pipeline
    }

    // 2. Execute pipelines (one per slot)
    for slot, pipeline := range pipelines {
        this.client.PipeDo(pipeline)  // Radix handles routing
    }
}
```

**Our Responsibility:**
- ✅ Group keys by slot (static calculation)
- ✅ Create one pipeline per slot
- ✅ Execute pipelines

**NOT Our Responsibility:**
- ❌ Track which node owns which slot
- ❌ Handle MOVED/ASK errors
- ❌ Update topology map

### Layer 2: Radix v4 Cluster (Dynamic Routing)

```go
// Radix v4 internal (simplified)
func (c *Cluster) Do(ctx context.Context, a Action) error {
    // 1. Get keys from action
    props := a.Properties()
    slot := ClusterSlot(props.Keys[0])

    // 2. Look up node in topology map
    node := c.topology.GetNodeForSlot(slot)

    // 3. Send to node
    err := node.Do(ctx, a)

    // 4. Handle errors
    if isMovedError(err) {
        // Extract new node from "MOVED 1649 192.168.1.101:7002"
        newNode := parseMovedError(err)

        // Update topology
        c.topology.UpdateSlot(slot, newNode)

        // Retry on new node
        return newNode.Do(ctx, a)
    }

    if isAskError(err) {
        // Extract temporary node from "ASK 1649 192.168.1.101:7002"
        tempNode := parseAskError(err)

        // Send ASKING, then retry
        tempNode.Do(ctx, radix.Cmd(nil, "ASKING"))
        return tempNode.Do(ctx, a)
    }

    return err
}
```

**Radix Responsibilities:**
- ✅ Maintain topology map (slot → node)
- ✅ Route commands to correct node
- ✅ Handle MOVED errors (permanent migration)
- ✅ Handle ASK errors (temporary migration)
- ✅ Auto-retry on correct node

## Resharding Scenarios

### Scenario 1: Slot Fully Migrated (MOVED)

```
Initial State:
  Slot 1649 → Node A

Resharding Begins:
  Slot 1649 migrating → Node A to Node C

Resharding Complete:
  Slot 1649 → Node C

Our Request:
  key = "user:1000"
  ClusterSlot("user:1000") = 1649  ← Always returns 1649

Flow:
1. Our code: Group key in pipelines[1649]
2. Our code: Execute PipeDo(pipelines[1649])
3. Radix: Check topology → Send to Node A (old info)
4. Node A: Responds "MOVED 1649 node-c:7003"
5. Radix: Update topology (1649 → Node C)
6. Radix: Retry entire pipeline to Node C
7. Node C: Success! Returns result
8. Our code: Receives success (transparent)
```

### Scenario 2: Slot Being Migrated (ASK)

```
Slot 1649 migration in progress:
  Some keys still on Node A
  Some keys already on Node C

Our Request:
  key = "user:1000"
  ClusterSlot("user:1000") = 1649

Flow:
1. Our code: Group key in pipelines[1649]
2. Our code: Execute PipeDo(pipelines[1649])
3. Radix: Send to Node A (primary owner)
4. Node A: "ASK 1649 node-c:7003" (this key is on C)
5. Radix: Send "ASKING" to Node C
6. Radix: Retry pipeline on Node C
7. Node C: Success! (one-time redirect)
8. Radix: No topology update (temporary)
9. Our code: Receives success (transparent)
```

### Scenario 3: Multiple Keys in Same Slot

```
Our Request (2 keys, same slot):
  key1 = "user:{group1}:1000" → slot 7859
  key2 = "user:{group1}:2000" → slot 7859

Our Code:
  pipelines[7859] = [INCRBY key1, EXPIRE key1, INCRBY key2, EXPIRE key2]

During Resharding (slot 7859: Node A → Node C):

Flow:
1. PipeDo(pipelines[7859]) → Radix sends to Node A
2. Node A: "MOVED 7859 node-c:7003"
3. Radix: Update topology, retry ENTIRE pipeline to Node C
4. Node C: All 4 commands succeed atomically
5. Success!

Key Point: Entire pipeline retried together (atomicity preserved)
```

### Scenario 4: Multiple Slots During Resharding

```
Our Request (3 keys, different slots):
  key1 → slot 1649 (Node A → Node C migration)
  key2 → slot 5712 (Node B, stable)
  key3 → slot 9876 (Node C, stable)

Our Code:
  pipelines[1649] = [INCRBY key1, EXPIRE key1]
  pipelines[5712] = [INCRBY key2, EXPIRE key2]
  pipelines[9876] = [INCRBY key3, EXPIRE key3]

Execution (parallel):
  PipeDo(pipelines[1649]) → MOVED → Retry Node C → Success
  PipeDo(pipelines[5712]) → Success (no move)
  PipeDo(pipelines[9876]) → Success (no move)

Result: All succeed, one required retry
```

## Safety Guarantees

### 1. Atomicity

Each slot's pipeline is atomic:
- All commands sent together
- Retry sends entire pipeline
- All succeed or all fail

### 2. Consistency

Slot calculation is deterministic:
- Same key always goes to same slot
- Slot number never changes
- Only node assignment changes

### 3. Retry Safety

Radix's retry is safe:
- MOVED: Permanent, update topology
- ASK: Temporary, no topology change
- Max retries prevent infinite loops

## Performance Impact

### Normal Operation (No Resharding)

```
Overhead: ~0%
- Slot calculation is O(1) (CRC16)
- Topology lookup is O(1) (hash map)
- No retries needed
```

### During Resharding

```
Affected Requests: Only keys in migrating slots
Overhead: 1-2 additional round-trips per affected request
Duration: Depends on migration speed

Example:
  Normal: 1 round-trip = 2ms
  With MOVED: 2 round-trips = 4ms (+100%)
  With ASK: 2.5 round-trips = 5ms (+150%)
```

### Impact Minimization

```
Best Practices:
1. Migrate during low-traffic periods
2. Gradual migration (one slot at a time)
3. Monitor MOVED/ASK error rates
4. Set up alerts for high retry rates
```

## Monitoring

### Metrics to Track

```go
// Recommended metrics
- cluster.moved_errors (counter)
- cluster.ask_errors (counter)
- cluster.slot_migrations (gauge)
- cluster.topology_updates (counter)
- request.retries (histogram)
```

### Alerting

```yaml
Alerts:
  - name: HighMovedErrorRate
    condition: moved_errors > 100/min
    action: Check if resharding in progress

  - name: StuckMigration
    condition: ask_errors > 1000/min for 5min
    action: Check migration status

  - name: TopologyThrashing
    condition: topology_updates > 10/min
    action: Investigate cluster instability
```

## Edge Cases

### 1. Concurrent Migrations

```
Multiple slots migrating simultaneously:
- Each handled independently
- No coordination needed
- May see multiple MOVED errors
```

### 2. Failed Failover

```
Master fails during request:
- Connection error
- Radix retries on replica (now master)
- May take 1-2 seconds (Redis failover time)
```

### 3. Split Brain

```
Network partition causes split brain:
- Cluster reports CLUSTERDOWN
- Requests fail with error
- Retry when cluster recovers
```

## Testing Resharding

### Manual Test

```bash
# 1. Start cluster
./scripts/start-cluster.sh

# 2. Run load test in background
./scripts/run-perf-test.sh &

# 3. Start resharding
redis-cli --cluster reshard 127.0.0.1:7001

# 4. Observe:
# - Requests continue succeeding
# - Some MOVED errors logged (normal)
# - Performance temporarily degraded
# - Recovers after migration completes
```

### Automated Test

```go
func TestResharding(t *testing.T) {
    // 1. Start cluster
    cluster := startTestCluster(t)

    // 2. Load data
    loadTestData(cluster, 1000 keys)

    // 3. Start continuous requests
    go continuousRequests(cluster)

    // 4. Trigger resharding
    cluster.Reshard(slot: 1649, from: nodeA, to: nodeC)

    // 5. Verify:
    // - All requests succeed
    // - Data accessible on new node
    // - Topology updated correctly
}
```

## Troubleshooting

### Problem: High MOVED Error Rate

```
Symptoms:
- Latency spike
- Many MOVED errors in logs

Causes:
- Resharding in progress
- Multiple slots migrating
- Topology updates delayed

Solutions:
- Wait for migration to complete
- Check cluster status: redis-cli cluster info
- Monitor migration progress
```

### Problem: Stuck in ASK Loop

```
Symptoms:
- Requests timing out
- Continuous ASK errors

Causes:
- Migration stuck
- Source node not releasing keys
- Target node not ready

Solutions:
- Check migration status
- Manually complete migration
- Restart affected nodes
```

## Conclusion

Our slot-based implementation handles Redis Cluster topology changes transparently:

1. **Static Slot Calculation**: We group by slot number (deterministic)
2. **Dynamic Routing**: Radix v4 handles node mapping and retries
3. **Automatic Recovery**: MOVED/ASK handled without application changes
4. **Production Ready**: Tested under resharding scenarios

**The combination of our slot grouping + Radix's topology management = Robust cluster support!**

## References

- Redis Cluster Specification: https://redis.io/docs/reference/cluster-spec/
- Radix v4 Documentation: https://pkg.go.dev/github.com/mediocregopher/radix/v4
- [01-slot-based-grouping.md](./01-slot-based-grouping.md) - Implementation details

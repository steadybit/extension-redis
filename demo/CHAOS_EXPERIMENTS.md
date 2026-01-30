# Redis Chaos Engineering Experiments

This guide provides real-world chaos engineering experiments for Redis using the Steadybit extension. Experiments are organized by persona (End-User Impact vs SRE/Platform) and include hypotheses, expected outcomes, and remediation strategies.

## Table of Contents

- [Setup](#setup)
- [Part 1: User-Facing Scenarios](#part-1-user-facing-scenarios)
  - [Experiment 1: Cache Unavailability](#experiment-1-cache-unavailability)
  - [Experiment 2: Session Loss](#experiment-2-session-loss)
  - [Experiment 3: Slow Cache Response](#experiment-3-slow-cache-response)
  - [Experiment 4: Cache Stampede](#experiment-4-cache-stampede)
- [Part 2: SRE/Platform Scenarios](#part-2-sreplatform-scenarios)
  - [Experiment 5: Connection Pool Exhaustion](#experiment-5-connection-pool-exhaustion)
  - [Experiment 6: Memory Pressure & Eviction](#experiment-6-memory-pressure--eviction)
  - [Experiment 7: Replication Lag](#experiment-7-replication-lag)
  - [Experiment 8: Redis Failover](#experiment-8-redis-failover)
- [Part 3: Combined Scenarios](#part-3-combined-scenarios)
  - [Experiment 9: Gradual Degradation](#experiment-9-gradual-degradation)
  - [Experiment 10: Full Chaos Game Day](#experiment-10-full-chaos-game-day)

---

## Setup

### Start the Demo Environment

```bash
cd demo
docker-compose up -d
```

This starts:
- **redis-master**: Primary Redis instance (port 6379)
- **redis-replica**: Replica for HA testing (port 6380)
- **demo-app**: Sample application using Redis (port 3400)
- **load-generator**: Continuous traffic generator

### Verify the Environment

```bash
# Check Redis
docker exec redis-master redis-cli -a dev-password INFO replication

# Check application
curl http://localhost:3400/health
curl http://localhost:3400/stats

# Test cache operations
curl http://localhost:3400/user/1
curl http://localhost:3400/product/5
curl http://localhost:3400/leaderboard
```

### Configure Extension

```bash
export STEADYBIT_EXTENSION_ENDPOINTS_JSON='[
  {"url":"redis://localhost:6379","password":"dev-password","name":"redis-master"},
  {"url":"redis://localhost:6380","password":"dev-password","name":"redis-replica"}
]'
go run .
```

---

## Part 1: User-Facing Scenarios

These experiments focus on how Redis issues affect end users.

### Experiment 1: Cache Unavailability

**Scenario**: Redis becomes temporarily unavailable. How does the application handle cache failures?

**Hypothesis**: When Redis is paused, the application should:
1. Fall back to direct database queries
2. Return slower but correct responses
3. Not show errors to users

#### Attack Configuration

| Parameter | Value |
|-----------|-------|
| Attack | Client Pause |
| Duration | 30s |
| Pause Mode | ALL |

#### Checks to Run (using HTTP Extension)

| Check | Threshold | Purpose |
|-------|-----------|---------|
| HTTP GET /health | Status 503 expected | Detect Redis dependency in health check |
| HTTP GET /user/1 | Timeout (5s+) | Detect missing fallback mechanism |

#### Actual Behavior (Resilience Gap Identified)

**Current demo app behavior:**
- `/health` returns **503** with ~2s response time (blocks waiting for Redis)
- `/user/1` **times out** after 5s (no fallback, no circuit breaker)
- Application does NOT gracefully degrade

**This experiment reveals the app lacks:**
- Circuit breaker pattern
- Timeout configuration on Redis client
- Fallback mechanism for cache misses

#### Remediation Strategies

1. **Circuit Breaker**: Implement circuit breaker pattern to fail fast (e.g., go-resilience, sony/gobreaker)
2. **Redis Client Timeout**: Configure read/write timeouts (e.g., 500ms)
3. **Fallback Cache**: Use local in-memory cache as L1
4. **Graceful Degradation**: Return cached/stale data or default values
5. **Health Check Isolation**: Don't let Redis block the health endpoint

#### Steadybit Experiment

```yaml
name: "Cache Unavailability - User Impact"
hypothesis: "Users experience slower responses but no errors when cache is unavailable"
attacks:
  - action: com.steadybit.extension_redis.instance.client-pause
    parameters:
      duration: "30s"
      pauseMode: "ALL"
checks:
  # This check will FAIL - demonstrating the resilience gap
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/user/1"
      method: "GET"
      duration: "45s"
      successRate: 0
      maxResponseTime: "10s"
  # Health check returns 503 during Redis unavailability
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/health"
      method: "GET"
      duration: "45s"
      expectedStatusCodes: [503]
      maxResponseTime: "5s"
```

#### Post-Remediation Target

After implementing fixes, update checks to:
```yaml
checks:
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/user/1"
      method: "GET"
      duration: "45s"
      successRate: 100
      maxResponseTime: "1s"
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/health"
      method: "GET"
      duration: "45s"
      successRate: 100
      maxResponseTime: "500ms"
```

---

### Experiment 2: Session Loss

**Scenario**: User sessions stored in Redis are deleted. How does the application handle session loss?

**Hypothesis**: When sessions are deleted:
1. Users should be gracefully logged out
2. No sensitive data exposure
3. Users can re-authenticate seamlessly

#### Attack Configuration

| Parameter | Value | Notes |
|-----------|-------|-------|
| Attack | Delete Keys | Targets **database** (not instance) |
| Target | db0 | Select the correct database in Steadybit |
| Pattern | `session:*` | Default is `steadybit-test:*` - must change! |
| Max Keys | 50 | |
| Restore on Stop | **false** | Default is `true` - keys are restored when attack stops! |

> **Important**: This attack targets a Redis **database** (db0, db1, etc.), not an instance. Make sure to select the correct database target in Steadybit where sessions are stored.

#### Pre-Attack Verification

Before running the attack, verify sessions exist:
```bash
# Create a test session
curl -X POST http://localhost:3400/session/user-1

# Verify it exists (should return 200 with session data)
curl http://localhost:3400/session/user-1

# Check the key exists in Redis
docker exec redis-master redis-cli -a dev-password KEYS "session:*"
```

#### Checks to Run (using HTTP Extension)

| Check | Threshold | Purpose |
|-------|-----------|---------|
| HTTP GET /session/user-1 | Status 404 | Confirm sessions are deleted |
| HTTP POST /session/new-user | Status 201 | Verify new sessions can be created |

#### Steadybit Experiment

```yaml
name: "Session Loss - Graceful Logout"
hypothesis: "Users are gracefully logged out when sessions are deleted"
attacks:
  - action: com.steadybit.extension_redis.database.key-delete
    parameters:
      pattern: "session:*"
      maxKeys: 50
      restoreOnStop: false  # IMPORTANT: Default is true!
checks:
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/session/user-1"
      method: "GET"
      duration: "30s"
      expectedStatusCodes: [404]
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/health"
      method: "GET"
      duration: "30s"
      successRate: 100
```

#### Post-Attack Verification

After the attack completes, verify:
```bash
# Session should be gone (404)
curl -v http://localhost:3400/session/user-1

# Keys should be deleted in Redis
docker exec redis-master redis-cli -a dev-password KEYS "session:*"

# Can create new session (201)
curl -X POST http://localhost:3400/session/new-user
```

#### Troubleshooting

If sessions are NOT deleted:
1. **Check `restoreOnStop`** - Default is `true`, keys are restored when attack ends
2. **Check target database** - Attack targets db0/db1/etc., not the instance
3. **Check pattern** - Default is `steadybit-test:*`, must change to `session:*`
4. **Check attack messages** - Steadybit shows "Deleted X keys" or "No keys found"

#### Remediation Strategies

1. **Session Persistence**: Store sessions in both Redis and database
2. **Session Regeneration**: Automatically create new sessions on miss
3. **Sticky Sessions**: Use cookies as backup session identifier

---

### Experiment 3: Slow Cache Response

**Scenario**: Redis responses are slow due to large key operations or network issues.

**Hypothesis**: When Redis latency increases:
1. Application timeout handling works correctly
2. Requests don't pile up
3. System recovers when latency returns to normal

#### Attack Configuration

| Parameter | Value | Notes |
|-----------|-------|-------|
| Attack | Big Key Creation | |
| Key Size | 10 MB | Demo Redis has 100MB maxmemory limit |
| Number of Keys | 5 | Total: 50MB (within 100MB limit) |
| Duration | 60s | |

> **Note**: Demo Redis is configured with `--maxmemory 100mb`. Ensure total key size (keySize × numKeys) stays under this limit. If keys fail to create, reduce the size.

#### Checks to Run (using HTTP Extension)

| Check | Threshold | Purpose |
|-------|-----------|---------|
| HTTP GET /user/1 | Max 500ms response | Detect latency increase |
| HTTP GET /health | 100% success | Ensure app stays healthy |
| Redis Memory Check | Max 80% | Monitor memory impact |

#### Expected Behavior

- Latency spikes during big key operations
- Some requests may timeout
- Memory usage increases significantly
- System recovers after attack (keys cleaned up)

#### Steadybit Experiment

```yaml
name: "Slow Cache Response - Big Keys"
hypothesis: "Application handles slow Redis responses gracefully"
attacks:
  - action: com.steadybit.extension_redis.instance.big-key
    parameters:
      keySize: 10        # 10 MB per key (demo limit is 100MB)
      numKeys: 5         # Total: 50MB
      duration: "60s"
checks:
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/user/1"
      method: "GET"
      duration: "60s"
      successRate: 95
      maxResponseTime: "2s"
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/health"
      method: "GET"
      duration: "60s"
      successRate: 100
```

#### Troubleshooting

If "Created 0 big keys" appears:
- Reduce `keySize` or `numKeys` - total must be under 100MB
- Check Redis memory: `docker exec redis-master redis-cli -a dev-password INFO memory`

---

### Experiment 4: Cache Stampede

**Scenario**: Many cache keys expire simultaneously, causing a "thundering herd" to the database.

**Hypothesis**: When cache keys expire en masse:
1. Database can handle the load spike
2. Cache repopulates within acceptable time
3. No cascading failures

#### Attack Configuration

| Parameter | Value |
|-----------|-------|
| Attack | Cache Expiration |
| Pattern | `user:*` |
| TTL | 1 second |
| Max Keys | 100 |

#### Checks to Run (using HTTP Extension)

| Check | Threshold | Purpose |
|-------|-----------|---------|
| HTTP GET /user/* | Max 500ms, 100% success | Ensure users get responses |
| HTTP GET /stats | 200 status | Monitor application stats |
| Redis Cache Hit Rate | Min 50% | Monitor recovery |

#### Test Procedure

1. Ensure cache is warm using HTTP extension or:
   ```bash
   for i in {1..100}; do curl http://localhost:3400/user/$i; done
   ```
2. Check hit rate via HTTP GET `http://localhost:3400/stats`
3. Run attack
4. Monitor HTTP response times during recovery
5. Verify no 5xx errors via HTTP extension checks

#### Steadybit Experiment

```yaml
name: "Cache Stampede - Thundering Herd"
hypothesis: "Application handles mass cache expiration without cascading failures"
attacks:
  - action: com.steadybit.extension_redis.database.cache-expiration
    parameters:
      pattern: "user:*"
      ttl: 1
      maxKeys: 100
checks:
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/user/1"
      method: "GET"
      duration: "60s"
      successRate: 100
      maxResponseTime: "500ms"
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/stats"
      method: "GET"
      duration: "60s"
      successRate: 100
```

#### Remediation Strategies

1. **Jittered Expiration**: Add random jitter to TTLs
2. **Cache Locking**: Use distributed locks for cache population
3. **Stale-While-Revalidate**: Serve stale data while refreshing

---

## Part 2: SRE/Platform Scenarios

These experiments focus on infrastructure resilience and operational concerns.

### Experiment 5: Connection Pool Exhaustion

**Scenario**: Redis connection pool is exhausted, simulating connection leaks or high load.

**Hypothesis**: When connections are exhausted:
1. New connection attempts fail gracefully
2. Existing connections continue to work
3. Monitoring alerts fire
4. System recovers when connections are released

#### Attack Configuration

| Parameter | Value |
|-----------|-------|
| Attack | Connection Exhaustion |
| Number of Connections | 80 |
| Duration | 60s |

#### Checks to Run (using HTTP Extension)

| Check | Threshold | Purpose |
|-------|-----------|---------|
| HTTP GET /health | 100% success | Verify app remains responsive |
| HTTP GET /user/1 | Max 200ms | Detect connection pool delays |
| Redis Connection Count | Max 90% | Alert before exhaustion |
| Redis Blocked Clients | Max 10 | Detect queuing |

#### Steadybit Experiment

```yaml
name: "Connection Pool Exhaustion"
hypothesis: "Application handles connection exhaustion gracefully"
attacks:
  - action: com.steadybit.extension_redis.instance.connection-exhaustion
    parameters:
      numConnections: 80
      duration: "60s"
checks:
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/health"
      method: "GET"
      duration: "60s"
      successRate: 95
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/user/1"
      method: "GET"
      duration: "60s"
      successRate: 90
      maxResponseTime: "500ms"
```

#### Expected Behavior

- `connected_clients` reaches near `maxclients`
- New connections may be rejected
- Existing operations continue
- Alert triggered on connection threshold

#### Monitoring Queries

```bash
# Check connection count
docker exec redis-master redis-cli -a dev-password INFO clients | grep connected

# Check rejected connections
docker exec redis-master redis-cli -a dev-password INFO stats | grep rejected
```

#### Remediation Strategies

1. **Connection Pooling**: Implement proper connection pooling in app
2. **Connection Limits**: Set per-app connection limits
3. **Auto-scaling**: Scale Redis or add read replicas

---

### Experiment 6: Memory Pressure & Eviction

**Scenario**: Redis runs out of memory, triggering eviction or OOM errors.

**Hypothesis**: When memory is constrained:
1. Eviction policy works as configured
2. Critical data is preserved (if using volatile-* policy)
3. Write operations fail gracefully with OOM errors
4. Monitoring alerts on memory threshold

#### Attack Configuration

**Scenario A: Force Eviction**

| Parameter | Value |
|-----------|-------|
| Attack | MaxMemory Limit |
| Max Memory | 10mb |
| Eviction Policy | allkeys-lru |
| Duration | 60s |

**Scenario B: Block Writes (OOM)**

| Parameter | Value |
|-----------|-------|
| Attack | MaxMemory Limit |
| Max Memory | 5mb |
| Eviction Policy | noeviction |
| Duration | 30s |

#### Checks to Run (using HTTP Extension)

| Check | Threshold | Purpose |
|-------|-----------|---------|
| HTTP GET /user/1 | 100% success | Reads should still work |
| HTTP POST /session/test | May fail with OOM | Write handling test |
| HTTP GET /health | 100% success | App health monitoring |
| Redis Memory Check | Max 90% | Early warning |

#### Steadybit Experiment (Scenario A - Eviction)

```yaml
name: "Memory Pressure - Eviction Policy"
hypothesis: "Application handles cache eviction gracefully with degraded hit rate"
attacks:
  - action: com.steadybit.extension_redis.instance.maxmemory-limit
    parameters:
      maxmemory: "10mb"
      evictionPolicy: "allkeys-lru"
      duration: "60s"
checks:
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/user/1"
      method: "GET"
      duration: "60s"
      successRate: 100
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/health"
      method: "GET"
      duration: "60s"
      successRate: 100
```

#### Steadybit Experiment (Scenario B - OOM)

```yaml
name: "Memory Pressure - OOM Errors"
hypothesis: "Application handles Redis OOM errors without crashing"
attacks:
  - action: com.steadybit.extension_redis.instance.maxmemory-limit
    parameters:
      maxmemory: "5mb"
      evictionPolicy: "noeviction"
      duration: "30s"
checks:
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/user/1"
      method: "GET"
      duration: "30s"
      successRate: 100
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/health"
      method: "GET"
      duration: "30s"
      successRate: 100
```

#### Expected Behavior

**Scenario A (Eviction)**:
- Least recently used keys are evicted
- Cache hit rate drops as keys are evicted
- No errors, but data loss occurs

**Scenario B (OOM)**:
- Write operations return OOM error
- Read operations continue to work
- Application should handle write failures gracefully

#### Monitoring Queries

```bash
# Check memory
docker exec redis-master redis-cli -a dev-password INFO memory

# Check evictions
docker exec redis-master redis-cli -a dev-password INFO stats | grep evicted
```

---

### Experiment 7: Replication Lag

**Scenario**: Redis replica falls behind the master, causing stale reads.

**Hypothesis**: When replication lag occurs:
1. Monitoring detects the lag
2. Reads from replica return stale data
3. Failover decisions account for lag
4. System recovers when lag clears

#### Attack Configuration

Use **Memory Fill** on master to create write load:

| Parameter | Value |
|-----------|-------|
| Attack | Memory Fill |
| Fill Rate | 50 MB/s |
| Max Memory | 50 MB |
| Duration | 30s |

#### Checks to Run (using HTTP Extension)

| Check | Threshold | Purpose |
|-------|-----------|---------|
| HTTP GET /user/1 | 100% success | User requests handled |
| HTTP GET /health | 100% success | App remains healthy |
| Redis Replication Lag | Max 5s | Detect sync delays |

#### Steadybit Experiment

```yaml
name: "Replication Lag - Stale Reads"
hypothesis: "System detects replication lag and handles stale reads"
attacks:
  - action: com.steadybit.extension_redis.instance.memory-fill
    parameters:
      fillRate: 50
      maxMemory: 50
      duration: "30s"
checks:
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/health"
      method: "GET"
      duration: "45s"
      successRate: 100
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/user/1"
      method: "GET"
      duration: "45s"
      successRate: 100
```

#### Test Procedure

1. Check initial replication status:
   ```bash
   docker exec redis-replica redis-cli -a dev-password INFO replication
   ```
2. Run memory fill attack on master
3. Monitor `master_last_io_seconds_ago` on replica
4. Verify HTTP requests continue to succeed
5. Verify lag clears after attack

#### Remediation Strategies

1. **Read-Your-Writes**: Route reads to master after writes
2. **Lag-Aware Routing**: Only route to replicas with acceptable lag
3. **Replica Scaling**: Add more replicas to distribute load

---

### Experiment 8: Redis Failover

**Scenario**: Primary Redis fails, requiring failover to replica.

**Hypothesis**: During failover:
1. Failover completes within SLA (e.g., 30 seconds)
2. Data loss is minimal (within acceptable RPO)
3. Applications reconnect automatically
4. No split-brain scenario

#### Test Procedure (Manual)

1. Verify replication is healthy:
   ```bash
   docker exec redis-replica redis-cli -a dev-password INFO replication
   ```

2. Stop the master:
   ```bash
   docker stop redis-master
   ```

3. Promote replica:
   ```bash
   docker exec redis-replica redis-cli -a dev-password REPLICAOF NO ONE
   ```

4. Verify promotion:
   ```bash
   docker exec redis-replica redis-cli -a dev-password INFO replication
   ```

5. Restart master as replica:
   ```bash
   docker start redis-master
   docker exec redis-master redis-cli -a dev-password REPLICAOF redis-replica 6379
   ```

#### Checks During Failover (using HTTP Extension)

| Check | Threshold | Purpose |
|-------|-----------|---------|
| HTTP GET /health | Min 80% success | Allow brief failures during failover |
| HTTP GET /user/1 | Max 5000ms | Allow for reconnection |

#### Steadybit Experiment

```yaml
name: "Redis Failover - HA Test"
hypothesis: "Application recovers within SLA when primary fails and replica is promoted"
checks:
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/health"
      method: "GET"
      duration: "120s"
      successRate: 80
  - action: com.steadybit.extension_http.check.periodically
    parameters:
      url: "http://localhost:3400/user/1"
      method: "GET"
      duration: "120s"
      successRate: 75
      maxResponseTime: "5s"
```

---

## Part 3: Combined Scenarios

### Experiment 9: Gradual Degradation

**Scenario**: Simulate gradual system degradation to test monitoring and alerting.

**Phases**:

| Time | Attack | Expected Impact |
|------|--------|----------------|
| 0-30s | Connection Exhaustion (50 conns) | Minor latency increase |
| 30-60s | Memory Fill (20MB) | Memory alerts |
| 60-90s | Cache Expiration (50 keys) | Hit rate drops |
| 90-120s | Client Pause (10s) | Service degradation |

**Purpose**: Verify that monitoring detects each phase and alerts escalate appropriately.

---

### Experiment 10: Full Chaos Game Day

**Objective**: Run a 1-hour chaos game day with random attacks.

**Rules**:
1. Each team member triggers one attack
2. On-call responds without knowing which attack
3. Measure Mean Time to Detect (MTTD) and Mean Time to Recover (MTTR)

**Attacks to Use**:
- Client Pause
- Memory Fill
- Connection Exhaustion
- Key Delete
- Cache Expiration
- MaxMemory Limit
- Big Key Creation

**Metrics to Track**:
- Time to detect issue
- Time to identify root cause
- Time to mitigate
- User impact (error rate, latency)

---

## Metrics Reference

### Key Redis Metrics to Monitor

| Metric | Source | Warning | Critical |
|--------|--------|---------|----------|
| `connected_clients` | INFO clients | >80% maxclients | >95% maxclients |
| `blocked_clients` | INFO clients | >10 | >50 |
| `used_memory` | INFO memory | >70% maxmemory | >90% maxmemory |
| `mem_fragmentation_ratio` | INFO memory | >1.5 | >2.0 |
| `evicted_keys` | INFO stats | >0/min | >100/min |
| `keyspace_hits/misses` | INFO stats | <80% hit rate | <50% hit rate |
| `master_link_status` | INFO replication | - | down |
| `master_last_io_seconds_ago` | INFO replication | >5s | >30s |

### Application Metrics

| Metric | Warning | Critical |
|--------|---------|----------|
| Cache hit rate | <90% | <70% |
| P99 latency | >100ms | >500ms |
| Error rate | >0.1% | >1% |
| Connection pool utilization | >80% | >95% |

---

## Cleanup

```bash
# Stop all containers
cd demo
docker-compose down -v

# Remove all demo data
docker volume prune
```

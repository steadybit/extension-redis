# Steadybit extension-redis

A [Steadybit](https://www.steadybit.com/) extension for Redis chaos engineering.

Learn about the capabilities of this extension in our [Reliability Hub](https://hub.steadybit.com/).

## Configuration

| Environment Variable | Required | Description |
|---------------------|----------|-------------|
| `STEADYBIT_EXTENSION_ENDPOINTS_JSON` | Yes | JSON array of Redis endpoint configurations |
| `STEADYBIT_EXTENSION_DISCOVERY_INTERVAL_INSTANCE_SECONDS` | No | Interval for instance discovery (default: 30) |
| `STEADYBIT_EXTENSION_DISCOVERY_INTERVAL_DATABASE_SECONDS` | No | Interval for database discovery (default: 60) |

### Endpoint Configuration

The `STEADYBIT_EXTENSION_ENDPOINTS_JSON` environment variable should contain a JSON array of Redis endpoint configurations:

```json
[
  {
    "url": "redis://localhost:6379",
    "password": "optional-password",
    "username": "optional-username",
    "db": 0,
    "name": "my-redis-instance",
    "insecureSkipVerify": false
  }
]
```

### TLS Configuration

For TLS connections, use the `rediss://` URL scheme:

```json
[
  {
    "url": "rediss://redis.example.com:6379",
    "password": "secret",
    "insecureSkipVerify": true
  }
]
```

## Supported Targets

### Redis Instance

Discovers Redis instances and exposes attributes like:
- `redis.host` - Redis host
- `redis.port` - Redis port
- `redis.version` - Redis version
- `redis.role` - Instance role (master/replica)
- `redis.cluster.enabled` - Cluster mode status
- `redis.memory.used_bytes` - Current memory usage
- `redis.connected_clients` - Connected client count

### Redis Database

Discovers Redis databases (db0-db15) and exposes:
- `redis.database.index` - Database index
- `redis.database.keys` - Key count in database
- `redis.database.name` - Database name (e.g., "db0")

## Supported Actions

### Attacks

#### Fill Memory
- **ID**: `com.steadybit.extension_redis.instance.memory-fill`
- **Target**: Instance
- **Description**: Fills Redis memory with random data to simulate memory pressure
- **Parameters**:
  - `duration` - How long to fill memory
  - `valueSize` - Size of each value in bytes (default: 10KB)
  - `fillRate` - Rate to fill in MB/s (default: 10)
  - `maxMemory` - Maximum memory to fill in MB (default: 100)

#### Delete Keys
- **ID**: `com.steadybit.extension_redis.database.key-delete`
- **Target**: Database
- **Description**: Deletes keys matching a pattern to simulate data loss
- **Parameters**:
  - `duration` - How long the attack lasts
  - `pattern` - Key pattern to match (e.g., "cache:*")
  - `maxKeys` - Maximum keys to delete (default: 100)
  - `restoreOnStop` - Restore deleted keys when attack stops (default: true)

#### Exhaust Connections
- **ID**: `com.steadybit.extension_redis.instance.connection-exhaustion`
- **Target**: Instance
- **Description**: Opens many connections to test connection limit handling
- **Parameters**:
  - `duration` - How long to hold connections
  - `numConnections` - Number of connections to open (default: 100)

#### Pause Clients
- **ID**: `com.steadybit.extension_redis.instance.client-pause`
- **Target**: Instance
- **Description**: Suspends all client command processing using CLIENT PAUSE
- **Parameters**:
  - `duration` - How long to pause clients
  - `pauseMode` - ALL (all commands) or WRITE (write commands only)
- **Reversibility**: Auto-reverts after timeout

#### Limit MaxMemory
- **ID**: `com.steadybit.extension_redis.instance.maxmemory-limit`
- **Target**: Instance
- **Description**: Reduces Redis maxmemory to force evictions or OOM errors
- **Parameters**:
  - `duration` - How long to apply the limit
  - `maxmemory` - Memory limit (e.g., "10mb", "1gb")
  - `evictionPolicy` - noeviction, allkeys-lru, allkeys-lfu, volatile-lru, volatile-ttl, or keep original
- **Reversibility**: Fully reversible - restores original settings on stop

#### Force Cache Expiration
- **ID**: `com.steadybit.extension_redis.database.cache-expiration`
- **Target**: Database
- **Description**: Sets TTL on keys matching a pattern to force expiration
- **Parameters**:
  - `duration` - Attack duration (for tracking)
  - `pattern` - Key pattern to match
  - `ttl` - TTL in seconds before keys expire (default: 5)
  - `maxKeys` - Maximum keys to affect (default: 100)
- **Reversibility**: Not reversible (keys expire permanently)

#### Create Big Keys
- **ID**: `com.steadybit.extension_redis.instance.big-key`
- **Target**: Instance
- **Description**: Continuously creates and deletes large keys to stress Redis memory handling
- **Parameters**:
  - `duration` - How long to run the big key cycles
  - `keySize` - Size per key in MB (default: 10)
  - `numKeys` - Number of big keys to create per cycle (default: 1)
- **Behavior**: Keys are created, held for 500ms, then deleted in continuous cycles throughout the attack duration
- **Reversibility**: Fully reversible - any remaining keys are deleted on stop
- **Note**: Total size per cycle (keySize × numKeys) must fit within Redis maxmemory limit. The attack will fail with an error if Redis cannot allocate memory for the keys.

### Checks

#### Memory Usage Check
- **ID**: `com.steadybit.extension_redis.instance.check-memory`
- **Target**: Instance
- **Description**: Monitors Redis memory usage and fails if threshold exceeded
- **Parameters**:
  - `duration` - Monitoring duration
  - `maxMemoryPercent` - Max memory as % of maxmemory (default: 80%)
  - `maxMemoryBytes` - Max memory in MB (optional)

#### Latency Check
- **ID**: `com.steadybit.extension_redis.instance.check-latency`
- **Target**: Instance
- **Description**: Monitors Redis response latency
- **Parameters**:
  - `duration` - Monitoring duration
  - `maxLatencyMs` - Maximum allowed latency in ms (default: 100)

#### Connection Count Check
- **ID**: `com.steadybit.extension_redis.instance.check-connections`
- **Target**: Instance
- **Description**: Monitors connected clients and fails if threshold exceeded
- **Parameters**:
  - `duration` - Monitoring duration
  - `maxConnectionsPct` - Max connections as % of maxclients (default: 80%)
  - `maxConnections` - Absolute max connections (optional)

#### Replication Lag Check
- **ID**: `com.steadybit.extension_redis.instance.check-replication`
- **Target**: Instance
- **Description**: Monitors Redis replication status and lag for replicas
- **Parameters**:
  - `duration` - Monitoring duration
  - `maxLagSeconds` - Maximum allowed replication lag (default: 10s)
  - `requireLinkUp` - Fail if master link is down (default: true)

#### Cache Hit Rate Check
- **ID**: `com.steadybit.extension_redis.instance.check-hitrate`
- **Target**: Instance
- **Description**: Monitors cache hit rate and fails if below threshold
- **Parameters**:
  - `duration` - Monitoring duration
  - `minHitRate` - Minimum acceptable hit rate % (default: 80%)

#### Blocked Clients Check
- **ID**: `com.steadybit.extension_redis.instance.check-blocked-clients`
- **Target**: Instance
- **Description**: Monitors blocked clients (waiting on BLPOP/BRPOP/etc.)
- **Parameters**:
  - `duration` - Monitoring duration
  - `maxBlockedClients` - Maximum allowed blocked clients (default: 10)

## Demo Environment & Chaos Experiments

A complete demo environment with a sample application and chaos engineering experiments is available in the `demo/` directory.

### Quick Start

```bash
cd demo
docker-compose up -d
```

This starts:
- **redis-master**: Primary Redis (port 6379)
- **redis-replica**: Replica for HA testing (port 6380)
- **demo-app**: Sample app with caching (port 3400)
- **load-generator**: Continuous traffic generator

### Chaos Experiments

See [demo/CHAOS_EXPERIMENTS.md](demo/CHAOS_EXPERIMENTS.md) for detailed chaos engineering scenarios including:

**User-Facing Scenarios:**
- Cache unavailability impact
- Session loss handling
- Slow cache response
- Cache stampede (thundering herd)

**SRE/Platform Scenarios:**
- Connection pool exhaustion
- Memory pressure & eviction
- Replication lag
- Redis failover

## Installation

### Helm

```bash
helm repo add steadybit https://steadybit.github.io/helm-charts
helm repo update
helm install steadybit-extension-redis steadybit/steadybit-extension-redis \
  --set redis.auth.managementEndpoints='[{"url":"redis://redis:6379"}]'
```

### Docker

```bash
docker run -d \
  -e STEADYBIT_EXTENSION_ENDPOINTS_JSON='[{"url":"redis://redis:6379"}]' \
  -p 8083:8083 \
  ghcr.io/steadybit/extension-redis:latest
```

## Development

### Prerequisites

- Go 1.25+
- Redis instance for testing
- Docker (for demo environment)

### Build

```bash
make build
```

### Test

```bash
# Unit tests only
go test ./clients/... ./config/... ./extredis/... -v

# All tests including e2e (requires minikube)
make test
```

### Run locally

```bash
# Start Redis
./scripts/start-redis.sh

# Run extension
export STEADYBIT_EXTENSION_ENDPOINTS_JSON='[{"url":"redis://localhost:6379","password":"dev-password"}]'
make run
```

## License

MIT License - see [LICENSE](LICENSE) for details.

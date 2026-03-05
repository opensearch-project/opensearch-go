# Life of a Client

This guide traces the Go client from initialization through steady-state operation to shutdown. It explains what background goroutines run, what caches are maintained, how connections move between pool partitions, and how the system responds to failures and recovery.

For the per-request routing pipeline, see [Life of a Request](request_lifecycle.md). For connection pool internals, see [Connection Pool](connection_pool.md).

## Overview

The client's lifecycle has four phases:

```
New()
  │
  ├── Parse config defaults (TLS, retry, timeouts, capacity model)
  ├── Create seed connections from Config.Addresses
  ├── Build connection pool (multiServerPool or singleServerPool)
  ├── Configure router + apply OPENSEARCH_GO_POLICY_* env overrides
  ├── Seed router with DiscoveryUpdate(conns)
  ├── Launch async health checks on seed URLs
  │
  v
Steady State (background goroutines)
  │
  ├── discoveryLoop                ── node + shard catalog refresh
  ├── scheduleNodeStats            ── JVM/breaker/thread-pool polling, load shedding
  ├── scheduleClusterHealthRefresh ── ready-node health probing
  └── scheduleResurrect (per conn) ── exponential-backoff health checks
  │
  v
Close()
  │
  ├── cancelFunc() -> ctx cancelled -> all goroutines exit
  └── transport.CloseIdleConnections()
```

## Startup

`opensearchtransport.New(cfg)` builds the client in a single synchronous call. The steps execute in order:

### 1. Configuration Resolution

Each setting follows the same resolution order: environment variable (operator override) > `Config` struct value (programmatic) > built-in default constant.

Key defaults:

| Setting                        | Default      | Env override                              |
| ------------------------------ | ------------ | ----------------------------------------- |
| Health check timeout           | 5s           | --                                        |
| Resurrection timeout (initial) | 5s           | --                                        |
| Resurrection timeout (max)     | 30s          | --                                        |
| Active list cap                | auto-derived | `OPENSEARCH_GO_ACTIVE_LIST_CAP`           |
| Node stats interval            | auto 5-30s   | `OPENSEARCH_GO_NODE_STATS_INTERVAL`       |
| Overloaded heap threshold      | 85%          | `OPENSEARCH_GO_OVERLOADED_HEAP_THRESHOLD` |
| Overloaded breaker ratio       | 0.90         | `OPENSEARCH_GO_OVERLOADED_BREAKER_RATIO`  |

### 2. Capacity Model

The client derives an initial capacity model from `defaultServerCoreCount` (8):

```
serverMaxNewConnsPerSec = serverCoreCount * 4.0  = 32
clientsPerServer        = serverCoreCount         = 8
healthCheckRate         = serverCoreCount * 2.0   = 16
```

Auto-discovery updates these when `/_nodes/_local/http,os` returns a node's `allocated_processors`. The capacity model drives the active list cap, resurrection rate limiting, and stats polling interval.

### 3. Connection Pool Construction

For multiple addresses, the client builds a `multiServerPool`:

```go
pool.mu.ready = shuffled(conns)     // All connections start active
pool.mu.activeCount = len(conns)
pool.mu.dead = []                   // Empty dead list
pool.enforceActiveCapWithLock()     // Move overflow to standby
```

For a single address, a lightweight `singleServerPool` is used instead.

### 4. Router Initialization (when `Config.Router` is set)

```
1. configurePolicySettings() -> propagate timeouts, health check func to all policy pools
2. parsePolicyOverrides()    -> read OPENSEARCH_GO_POLICY_* env vars
3. applyPolicyOverrides()    -> disable matching policies before first DiscoveryUpdate
4. DiscoveryUpdate(conns)    -> seed all policy pools with connections
5. go asyncHealthChecks()    -> parallel health checks, first success unblocks requests
```

The async health check uses `errgroup`: all seed URLs are probed in parallel, and the first success cancels the others. This provides fast startup even when some seed nodes are unreachable.

### 5. Background Goroutine Launch

Three goroutines are started at the end of `New()`:

```go
go client.discoveryLoop()              // if DiscoverNodesInterval > 0
go client.scheduleNodeStats()          // if nodeStatsInterval > 0
go client.scheduleClusterHealthRefresh() // if healthCheckRate > 0
```

All three derive a child context from `client.ctx`, so `Close()` cancels them all.

## Background Goroutines

### Summary

| Goroutine                      | Interval                             | HTTP calls                                     | Purpose                                              |
| ------------------------------ | ------------------------------------ | ---------------------------------------------- | ---------------------------------------------------- |
| `discoveryLoop`                | `DiscoverNodesInterval` (default 5m) | `/_nodes/http`, `/_cat/shards`                 | Topology + shard placement refresh, standby rotation |
| `scheduleNodeStats`            | Auto 5-30s                           | `/_nodes/_local/stats/jvm,breaker,thread_pool` | Load shedding, CWND updates                          |
| `scheduleClusterHealthRefresh` | Auto 5s-5m                           | `/_cluster/health?local=true` per node         | Readiness gate (initializing shards), cluster status |
| `scheduleResurrect` (per conn) | Exponential backoff + jitter         | `GET /`                                        | Dead connection recovery                             |

### Discovery Loop

A single `time.Timer` goroutine handles two event types:

**Full discovery** runs at the configured `DiscoverNodesInterval` (default 5 minutes):

```
DiscoverNodes()
  ├── getNodesInfo() -> GET /_nodes/http -> node list, roles, publish addresses
  ├── nodeDiscovery() -> diff against current pool, reuse or replace connections
  ├── updateConnectionPool() -> add/remove connections from all pools
  ├── fetchAndUpdateShardPlacement() -> GET /_cat/shards -> index routing cache
  ├── router.CheckDead() -> sync dead lists across policy pools
  └── rotateStandby() -> health-check one standby, swap with active
```

**Cat-only refresh** runs between full discoveries when shard placement may be stale:

```
fetchAndUpdateShardPlacement()
  └── GET /_cat/shards -> update indexRoutingCache, clear lcNeedsCatUpdate
```

Two atomic flags drive urgency beyond the regular interval:

| Flag               | Set by                                            | Effect                                       |
| ------------------ | ------------------------------------------------- | -------------------------------------------- |
| `catRefreshNeeded` | `requestCatRefresh()` on transport errors         | Schedules cat-only refresh within 5s         |
| `discoveryNeeded`  | `requestDiscoveryNow()` on large topology changes | Runs full discovery immediately on next wake |

The `minCatRefreshInterval` (5 seconds) rate-limits failure-triggered refreshes to prevent a burst of `/_cat/shards` calls during a cascading failure.

### Node Stats Poller

Polls each live node's JVM heap, circuit breakers, and thread pool stats:

```
pollNodeStats()
  └── for each ready connection:
        GET /_nodes/_local/stats/jvm,breaker,thread_pool
        ├── heap_used_percent >= 85         -> demoteOverloaded(conn)
        ├── breaker estimated/limit >= 0.90 -> demoteOverloaded(conn)
        ├── breaker tripped count increased -> demoteOverloaded(conn)
        ├── cluster health red              -> demoteOverloaded(conn)
        ├── thread_pool rejected count      -> CWND multiplicative decrease
        └── all clear                       -> promoteFromOverloaded(conn) if flagged
```

The polling interval auto-scales with cluster size: `clamp(liveNodes * clientsPerServer / healthCheckRate, 5s, 30s)`.

### Cluster Health Refresh

Probes each ready connection with `GET /_cluster/health?local=true`:

- If `initializing_shards > 0`, the node is not yet ready -- it remains in the dead list and is retried next cycle
- Provides cluster status (`green`/`yellow`/`red`) used by the stats poller for overload detection
- Interval auto-scales: `clamp(liveNodes * clientsPerServer / healthCheckRate, 5s, 5min)`

### Per-Connection Resurrection

When a connection is marked dead (via `OnFailure`), `scheduleResurrect` spawns a goroutine that retries health checks with exponential backoff:

```
scheduleResurrect(conn)
  └── loop:
        timeout = calculateResurrectTimeout(conn)
        sleep(timeout)
        performHealthCheck(conn)
        ├── fail -> increment failures, loop with longer timeout
        └── pass -> resurrectWithLock(conn) -> promote to Active, exit loop
```

The timeout computation uses three competing inputs (highest wins):

| Input                | Formula                                                  | Purpose                                                      |
| -------------------- | -------------------------------------------------------- | ------------------------------------------------------------ |
| Health-ratio timeout | `initial * 2^(failures-1) * (liveNodes/totalNodes)`      | Healthy clusters wait longer; degraded clusters retry sooner |
| Rate-limited timeout | `liveNodes * clientsPerServer / serverMaxNewConnsPerSec` | Throttles TLS handshake pressure on recovering servers       |
| Minimum floor        | `minimumResurrectTimeout` (500ms)                        | Absolute lower bound                                         |

Jitter is added to stagger retries across goroutines.

## Connection Lifecycle State Machine

Each connection's state is packed into a single atomic 64-bit word:

```
63      52 51           26 25            0
+----------+--------------+--------------+
|    LC    | warmupConfig | warmupState  |
+----------+--------------+--------------+
  12 bits      26 bits        26 bits
```

### Lifecycle Bits (top 12 bits)

| Group     | Bit   | Name               | Meaning                                             |
| --------- | ----- | ------------------ | --------------------------------------------------- |
| Readiness | 0x01  | `lcReady`          | Connection believed functional                      |
| Readiness | 0x02  | `lcUnknown`        | Status uncertain, needs health check                |
| Position  | 0x04  | `lcActive`         | In active partition, serving requests               |
| Position  | 0x08  | `lcStandby`        | In standby partition, idle                          |
| Metadata  | 0x10  | `lcNeedsWarmup`    | Needs warmup before full traffic                    |
| Metadata  | 0x20  | `lcOverloaded`     | Node under excessive load, parked                   |
| Metadata  | 0x40  | `lcHealthChecking` | Health check goroutine running                      |
| Metadata  | 0x80  | `lcDraining`       | HTTP/2 GOAWAY received                              |
| Extended  | 0x100 | `lcNeedsHardware`  | Needs `/_nodes/_local/http,os` call                 |
| Extended  | 0x200 | `lcNeedsCatUpdate` | Shard placement stale, excluded from scored routing |

When neither position bit is set, the connection is on the dead list. "Dead" is not a separate bit -- it is `lcUnknown` with no position.

### State Transitions

```
                  ┌───────────────┐
                  │  New / Dead   │ lcUnknown, no position
                  │  (dead list)  │
                  └───────┬───────┘
                          │ health check passes
                          │ resurrectWithLock()
                          v
               ┌──────────────────────┐
               │    Active            │ lcReady + lcActive
               │ (ready[0:active])    │<──────┐
               └──────┬───────┬───────┘       │
                      │       │               │ rotateStandby /
          OnFailure() │       │ active cap    │ deferredPromotion
                      │       │ overflow      │
                      v       v               │
               ┌──────────────────────┐       │
               │    Standby           │ lcReady + lcStandby
               │ (ready[active:len])  │───────┘
               └──────┬───────────────┘
                      │ OnFailure()
                      v
               ┌──────────────────────┐
               │  Dead                │ lcUnknown, no position
               │  scheduleResurrect() │──> (loops back to Active)
               └──────────────────────┘
```

Additional transition paths:

| Transition                           | Trigger                   | Notes                                                                      |
| ------------------------------------ | ------------------------- | -------------------------------------------------------------------------- |
| Active -> Standby (overloaded)       | `demoteOverloaded()`      | `lcOverloaded` flag set, no failure increment                              |
| Standby -> Active (overload cleared) | `promoteFromOverloaded()` | Stats poller clears `lcOverloaded`                                         |
| Any -> Dead (draining)               | HTTP/2 RST_STREAM         | `lcDraining` set, requires N consecutive health checks before resurrection |

### Warmup

Resurrected connections start with `lcNeedsWarmup`. The warmup system uses a non-linear ramp: the connection skips most requests initially (high `skipCount`), accepting only every Nth request. Each round halves the skip count until the connection is fully warmed. This prevents a resurrected node from being overwhelmed by a sudden traffic spike.

The lower 52 bits of `connState` encode two `warmupManager` values (config template + working state), each with an 8-bit `rounds` and 8-bit `skipCount`. Warmup rounds are scaled by pool size: small pools warm quickly (4 rounds), large pools ramp gradually (up to 16 rounds).

## Pool Partitions

Every `multiServerPool` manages connections in three partitions:

```
ready[0 : activeCount]    Active    Round-robin selection, warmup-aware
ready[activeCount : len]  Standby   Idle, health-checked before promotion
dead[]                    Dead      Exponential-backoff resurrection
```

When `Next()` is called:

1. **Active partition**: round-robin across `ready[0:activeCount]`, skip warmup connections probabilistically
2. **Standby partition**: if active is empty, emergency promote a standby connection
3. **Zombie fallback**: if ready is empty, rotate through dead list one at a time

The `activeListCap` limits how many connections serve traffic simultaneously. When discovery adds nodes beyond the cap, overflow connections go to standby. The cap is auto-derived from the capacity model: `serverMaxNewConnsPerSec * resurrectTimeoutInitial / clientsPerServer`.

For details on pool mechanics, see [Connection Pool](connection_pool.md).

## Cache and Catalog Updates

The client maintains seven caches, each with its own update and invalidation mechanism:

| Cache             | Updated by                                           | Stored in                           | Invalidated by                                         |
| ----------------- | ---------------------------------------------------- | ----------------------------------- | ------------------------------------------------------ |
| Node list + roles | `DiscoverNodes` -> `/_nodes/http`                    | `connectionPool.ready` + `dead`     | Next discovery cycle                                   |
| Hardware info     | Health check substitution (`/_nodes/_local/http,os`) | `conn.allocatedProcessors`          | `lcNeedsHardware` flag on new/replaced nodes           |
| Shard placement   | `fetchAndUpdateShardPlacement` -> `/_cat/shards`     | `indexRoutingCache` per index slot  | `lcNeedsCatUpdate` per conn, `catRefreshNeeded` atomic |
| RTT measurements  | `performHealthCheck` RTT recording                   | `conn.rttRing`                      | Ring buffer overwrites (size derived from intervals)   |
| Thread pool stats | `pollNodeStats` -> `/_nodes/_local/stats`            | `conn.poolRegistry` (CWND per pool) | Each poll cycle replaces                               |
| Cluster health    | `pollClusterHealth` -> `/_cluster/health?local=true` | `conn` health state                 | Each poll cycle replaces                               |
| Server version    | `performHealthCheck` response body parse             | `conn.version` (atomic string)      | Updated on each health check                           |

Hardware info uses a **lazy fetch** pattern: rather than calling `/_nodes/_local/http,os` on every discovery cycle, the `lcNeedsHardware` flag is set only when a connection is new or has been replaced. The next health check for that connection substitutes the heavier endpoint to capture `allocated_processors`.

Shard placement uses an **event-driven invalidation** pattern: transport errors set `lcNeedsCatUpdate` per-connection and `catRefreshNeeded` globally. The discovery loop wakes within 5 seconds to refresh `/_cat/shards`.

## Failure Response Timeline

When a node fails, the client responds across multiple time scales:

```
t=0s    Request fails (transport error)
        |-- OnFailure(conn) --> conn moves Active --> Dead
        |-- scheduleResurrect(conn) spawned
        |-- conn.setNeedsCatUpdate() --> excluded from scored routing
        +-- requestCatRefresh() --> atomic flag for discovery loop

t=0-5s  Remaining Active connections serve traffic
        Discovery loop wakes on catRefreshNeeded (5s floor)

t=5s    Cat-only refresh: /_cat/shards
        |-- Clears lcNeedsCatUpdate on healthy connections
        +-- Scored routing resumes for surviving nodes

t=5-30s scheduleResurrect backoff: calculateResurrectTimeout()
        |-- healthRatio timeout: initial * 2^(failures-1) * (live/total)
        |-- rateLimited timeout: liveNodes * clientsPerServer / maxNewConnsPerSec
        +-- floor: minimumResurrectTimeout (500ms default)

t=Xs    performHealthCheck --> GET /
        |-- Fail: increment failures, loop with longer timeout
        +-- Pass: resurrectWithLock() --> Dead --> Active (with warmup)

t=5m    Next full discovery cycle
        |-- /_nodes/http --> topology update (detects node removal/addition)
        |-- /_cat/shards --> shard placement refresh
        +-- rotateStandby --> health-check one standby, swap with active
```

During the gap between failure detection and resurrection, the client continues serving requests using the remaining active connections. If all active connections fail, the standby partition provides emergency capacity. If standby is also empty, the zombie fallback rotates through dead connections one at a time until a node recovers.

## Observability

The `ConnectionObserver` interface provides 14 callback methods for monitoring the client's internal state:

| Category           | Methods                                                       |
| ------------------ | ------------------------------------------------------------- |
| Pool transitions   | `OnPromote`, `OnDemote`                                       |
| Overload           | `OnOverloadDetected`, `OnOverloadCleared`                     |
| Discovery          | `OnDiscoveryAdd`, `OnDiscoveryRemove`, `OnDiscoveryUnchanged` |
| Health             | `OnHealthCheckPass`, `OnHealthCheckFail`                      |
| Standby            | `OnStandbyPromote`, `OnStandbyDemote`                         |
| Warmup             | `OnWarmupRequest`                                             |
| Routing            | `OnRoute`                                                     |
| Shard invalidation | `OnShardMapInvalidation`                                      |

Embed `BaseConnectionObserver` and override only the methods you need:

```go
type myObserver struct {
    opensearchtransport.BaseConnectionObserver
}

func (o *myObserver) OnDemote(event opensearchtransport.ConnectionEvent) {
    log.Printf("connection dead: %s", event.URL)
}

func (o *myObserver) OnRoute(event opensearchtransport.RouteEvent) {
    log.Printf("routed %s %s to %s (score=%.2f)",
        event.Method, event.Path, event.WinnerURL, event.WinnerScore)
}
```

Observer methods may be called while internal locks are held. Implementations must not call back into the Client or connection pool.

## Shutdown

`Close()` performs a clean shutdown:

```go
func (c *Client) Close() error {
    c.cancelFunc()                           // Cancel ctx -> all goroutines exit
    if t, ok := c.transport.(interface {
        CloseIdleConnections()
    }); ok {
        t.CloseIdleConnections()             // Close idle HTTP/TLS connections
    }
    return nil
}
```

The caller is responsible for stopping new requests before calling `Close()`. In-flight requests are not drained -- they complete (or fail) normally against the underlying `http.Transport`.

In tests, pass `t.Context()` as `Config.Context` so that all background goroutines are automatically cancelled when the test ends.

## Related Guides

- [Life of a Request](request_lifecycle.md) -- single-request routing walkthrough
- [Request Routing](request_routing.md) -- policy primitives, custom routers
- [Connection Pool](connection_pool.md) -- pool internals, warmup, standby
- [Node Discovery and Roles](node_discovery_and_roles.md) -- discovery mechanics
- [Cluster Health Checking](cluster_health_checking.md) -- health endpoints, permissions
- [Retry Backoff](retry_backoff.md) -- resurrection timeout formula
- [Connection Scoring](connection_scoring.md) -- RTT bucketing, CWND, shard cost

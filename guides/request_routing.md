# Request-Based Connection Routing

Request-based connection routing automatically directs operations to appropriate nodes based on request type and node roles, optimizing performance with graceful fallback.

## Quick Start

```go
import "github.com/opensearch-project/opensearch-go/v4/opensearchtransport"

// Recommended: affinity-aware smart router (production default)
router := opensearchtransport.NewSmartRouter()

client, err := opensearch.NewClient(opensearch.Config{
    Addresses:             []string{"https://localhost:9200"},
    DiscoverNodesOnStart:  true,
    DiscoverNodesInterval: 5 * time.Minute,
    Router:                router,
})
```

This provides role-based routing, per-index node affinity, RTT-based AZ preference, and dynamic fan-out. See [Affinity Routing](affinity_routing.md) for the full algorithm description.

Without a router, the client uses round-robin across all discovered nodes (the pre-existing default behavior).

## Pre-Built Routers

| Router           | Constructor             | Use Case                                                                     |
| ---------------- | ----------------------- | ---------------------------------------------------------------------------- |
| Smart (Affinity) | `NewSmartRouter()`      | **Production default.** Role-based + per-index affinity with RTT scoring.    |
| Mux              | `NewMuxRouter()`        | Role-based routing without affinity. Useful for debugging routing decisions. |
| Round-Robin      | `NewRoundRobinRouter()` | Coordinating-only preference with round-robin fallback. Simplest option.     |

`NewDefaultRouter()` currently returns the same router as `NewSmartRouter()`.

```go
// Role-based routing without affinity (useful for comparing behavior)
router := opensearchtransport.NewMuxRouter()

// Simple coordinating-only preference with round-robin fallback
router := opensearchtransport.NewRoundRobinRouter()
```

All three routers share the same policy chain structure:

1. **Coordinating-only nodes** (if any exist, routes all traffic to them exclusively)
2. **Operation-specific routing** (Smart/Mux only: bulk/reindex->ingest, search/msearch/count->search/data, scroll/PIT->search/data, refresh/flush/cache clear->data, stats/recovery->data, etc.)
3. **Round-robin fallback** (high availability)

## Routing Architecture

### Policies

The routing system uses a **chain-of-responsibility pattern**:

- **Router**: Top-level coordinator that tries policies in sequence
- **Policy**: Individual routing strategy that returns a connection pool or `(nil, nil)` to pass
- **Fallthrough**: Policies return `(nil, nil)` when they do not match, allowing the next policy to try

### Policy Primitives

```go
// Chain: try policies in sequence until one matches
opensearchtransport.NewPolicy(policy1, policy2, policy3)

// Role-based: nodes with specific roles
opensearchtransport.NewRolePolicy("data", "ingest")

// Coordinating-only: nodes with no explicit roles
opensearchtransport.NewRolePolicy(opensearchtransport.RoleCoordinatingOnly)

// Round-robin: all available nodes
opensearchtransport.NewRoundRobinPolicy()

// HTTP pattern matching: route based on method + path
opensearchtransport.NewMuxPolicy(routes)

// Conditional: evaluate a predicate at request time
opensearchtransport.NewIfEnabledPolicy(conditionFunc, truePolicy, falsePolicy)

// Null: always returns (nil, nil) -- used as a fallthrough terminator
opensearchtransport.NewNullPolicy()
```

## Custom Router Examples

The pre-built routers cover most use cases. Custom routers are useful for non-standard policy composition.

### Role Preference with Fallback

Route to data nodes when available, otherwise round-robin:

```go
dataPolicy, _ := opensearchtransport.NewRolePolicy("data")
router := opensearchtransport.NewRouter(
    dataPolicy,
    opensearchtransport.NewRoundRobinPolicy(),
)
```

### Composing Policies Manually

The pre-built `NewMuxRouter()` is equivalent to this explicit composition:

```go
coordinatingPolicy, _ := opensearchtransport.NewRolePolicy(opensearchtransport.RoleCoordinatingOnly)
routes := opensearchtransport.NewDefaultRoutes()
muxPolicy := opensearchtransport.NewMuxPolicy(routes)

router := opensearchtransport.NewRouter(
    opensearchtransport.NewIfEnabledPolicy(
        func(ctx context.Context, req *http.Request) bool {
            return coordinatingPolicy.IsEnabled()
        },
        coordinatingPolicy,
        opensearchtransport.NewNullPolicy(),
    ),
    muxPolicy,
    opensearchtransport.NewRoundRobinPolicy(),
)
```

This illustrates the building blocks: IfEnabled gates on coordinating-node availability, the mux policy handles operation-type routing, and round-robin provides the fallback safety net.

### Conditional Routing

IfEnabledPolicy evaluates a predicate at request time. The typical use is gating on whether a node-role pool has members (via `policy.IsEnabled()`), but the predicate can also inspect the request:

```go
ingestPolicy, _ := opensearchtransport.NewRolePolicy("ingest")
dataPolicy, _ := opensearchtransport.NewRolePolicy("data")

router := opensearchtransport.NewRouter(
    opensearchtransport.NewIfEnabledPolicy(
        func(ctx context.Context, req *http.Request) bool {
            return ingestPolicy.IsEnabled()
        },
        ingestPolicy,
        dataPolicy,
    ),
    opensearchtransport.NewRoundRobinPolicy(),
)
```

This routes to ingest nodes when they exist, falls back to data nodes when they do not, and round-robins if neither role has nodes.

## Cluster Examples

### Mixed Cluster (Optimized)

```yaml
nodes:
  - name: cluster-manager-1
    roles: [cluster_manager]
  - name: ingest-1
    roles: [ingest]
  - name: data-1
    roles: [data]
  - name: coordinator-1
    roles: [] # coordinating-only
```

With `NewSmartRouter()`:

- All requests -> coordinator-1 (coordinating-only nodes get exclusive traffic)

With `NewMuxRouter()` (if no coordinating-only nodes):

- `POST /_bulk` -> ingest-1
- `POST /my-index/_search` -> data-1
- `POST /my-index/_refresh` -> data-1
- `GET /_cluster/health` -> round-robin across all non-manager nodes

### Production Cluster with Search Nodes (OpenSearch 3.0+)

```yaml
nodes:
  - name: data-1
    roles: [data]
  - name: data-2
    roles: [data]
  - name: search-1
    roles: [search]
  - name: ingest-1
    roles: [ingest]
```

With `NewSmartRouter()`:

- `POST /_bulk` -> ingest-1 (with affinity)
- `POST /products/_search` -> search-1 (with per-index affinity)
- `GET /products/_doc/123` -> search-1 (with document affinity)
- `POST /products/_search/scroll` -> search-1 (with per-index affinity)
- `POST /products/_refresh` -> data-1 or data-2 (shard maintenance targets data nodes)

If search-1 is removed, search operations automatically fall back to data nodes.

### Single Node (Development)

```yaml
nodes:
  - name: opensearch-single
    roles: [cluster_manager, data, ingest]
```

- All policies match the same node
- No performance difference vs round-robin
- Same code works in development and production

## Health Check Configuration

For details on the health check endpoint -- response fields, HTTP status codes, required permissions, and security configuration -- see [cluster_health_checking.md](cluster_health_checking.md).

Configure health check behavior for discovered nodes:

```go
client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"https://localhost:9200"},

    // Health check probing
    HealthCheckTimeout:    5 * time.Second, // Per-request timeout (default: 5s)
    HealthCheckMaxRetries: 6,               // Max retries (default: 6, -1 = disable)
    HealthCheckJitter:     0.1,             // Retry jitter factor (default: 0.1)
})
```

### Resurrection Timeout Tuning

When a node fails, the client uses exponential backoff with cluster-health-aware rate limiting to schedule resurrection attempts. See [retry_backoff.md](retry_backoff.md) for the full algorithm and recovery timeline.

```go
client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"https://localhost:9200"},

    // Exponential backoff: initial * 2^(failures-1), capped at max
    ResurrectTimeoutInitial: 5 * time.Second,         // Default: 5s
    ResurrectTimeoutMax:     30 * time.Second,        // Default: 30s
    MinimumResurrectTimeout: 500 * time.Millisecond,  // Default: 500ms
    JitterScale:             0.5,                     // Default: 0.5
})
```

### Readiness Health Checks

When cluster health checking is available (the node permits `GET /_cluster/health?local=true`), the client applies a **two-phase readiness gate** before adding a node to the ready pool:

1. **Phase 1** -- `GET /` confirms HTTP connectivity and extracts the server version.
2. **Phase 2** -- `GET /_cluster/health?local=true` checks shard initialization status.

If `initializing_shards > 0`, the node remains in the dead list and is retried on the next health check cycle. This prevents routing traffic to a node that is reachable but still absorbing shard data after a restart.

When all nodes are recovering simultaneously (cold start), requests still succeed via the zombie-connection fallback: the pool rotates through dead connections one at a time until nodes finish initializing.

### Node Stats Polling and Load Shedding

A background goroutine polls each live node's JVM heap usage and circuit-breaker metrics to detect overloaded nodes and shed load away from them. **This is enabled by default** with automatically derived polling intervals.

```go
client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"https://localhost:9200"},

    // Defaults (load shedding is enabled out of the box):
    // NodeStatsInterval:       0,    // 0 = auto-derive from cluster size (5s-30s)
    // OverloadedHeapThreshold: 85,   // JVM heap % (default: 85)
    // OverloadedBreakerRatio:  0.90, // Breaker estimated/limit (default: 0.90)
})

// To disable load shedding:
client, err := opensearch.NewClient(opensearch.Config{
    Addresses:         []string{"https://localhost:9200"},
    NodeStatsInterval: -1, // Negative value disables polling
})
```

A node is marked overloaded if **any** of these are true:

- JVM `heap_used_percent` >= `OverloadedHeapThreshold`
- Any circuit breaker's `estimated_size / limit_size` >= `OverloadedBreakerRatio`
- Any circuit breaker's `tripped` count increased since the last poll
- Cluster health status is `"red"` (reuses data from cluster health checks; no extra HTTP call)

Overloaded nodes are demoted from the active partition to the standby partition with the `lcOverloaded` flag set. Unlike genuine failures, overloaded connections are not moved to the dead list and their failure counter is not incremented: the connection is healthy, but under excessive load. The stats poller clears the flag and promotes nodes back to active when metrics improve. When all active nodes are overloaded and demoted to standby, requests fall through to the standby partition (emergency promotion) or zombie fallback, providing natural backpressure.

**Environment variable overrides** allow tuning at deployment time without recompiling:

| Variable                                  | Format              | Default       | Description                                                       |
| ----------------------------------------- | ------------------- | ------------- | ----------------------------------------------------------------- |
| `OPENSEARCH_GO_NODE_STATS_INTERVAL`       | Duration or seconds | auto (5s-30s) | Stats polling interval. `0` or unset = auto. Negative = disabled. |
| `OPENSEARCH_GO_OVERLOADED_HEAP_THRESHOLD` | Integer (0-100)     | `85`          | JVM heap threshold. `100` = disable heap detection.               |
| `OPENSEARCH_GO_OVERLOADED_BREAKER_RATIO`  | Float (0.0-1.0)     | `0.90`        | Breaker ratio threshold. `1.0` = disable breaker detection.       |

## Performance

Routing decisions are evaluated in the hot path of every request. All policies and routers are designed to add minimal overhead with zero or near-zero heap allocations.

**Policy evaluation cost** ranges from ~145 ns (pass-through policies like NullPolicy, IfEnabledPolicy, and role misses) to ~580 ns (IndexAffinityPolicy with rendezvous hashing over 10 nodes). All policies evaluate at **0 allocs/op**.

**End-to-end router cost** for matched routes:

| Router      | Matched Route                  | ns/op | allocs/op |
| ----------- | ------------------------------ | ----- | --------- |
| MuxRouter   | Search, Bulk, Get, IndexSearch | ~155  | 0         |
| SmartRouter | Search, Bulk                   | ~220  | 0         |
| SmartRouter | IndexSearch                    | ~490  | 1         |
| SmartRouter | Get (document affinity)        | ~520  | 2         |

SmartRouter's additional latency over MuxRouter comes from rendezvous hashing and affinity scoring. The 1-2 allocations on Get/IndexSearch paths are from Go's `http.ServeMux` wildcard capture and `sync.Pool` miss rate, both internal to the Go standard library.

Routes that do not match any registered pattern (for example, `PUT /{index}/_doc/{id}`) fall through to round-robin. On the fallthrough path, Go's `http.ServeMux` enumerates allowed methods internally, which adds 18-35 allocations. This affects only unmatched routes and does not impact the primary Search, Bulk, and Get paths.

See `opensearchtransport/routing_benchmark_test.go` for the full benchmark suite and reference data.

## Troubleshooting

**Q: How do I verify routing is working?**

Enable debug logging to see which connections are selected by policies.

**Q: Can I disable request routing?**

Omit the `Router` field from `opensearch.Config` to use standard round-robin behavior.

**Q: Can I disable a specific routing policy at runtime?**

Yes. Set an `OPENSEARCH_GO_POLICY_<TYPE>` environment variable to disable specific policies without code changes. See [Policy Environment Variable Overrides](#policy-environment-variable-overrides) below.

**Q: Why do my role policies not match?**

Check that nodes have the expected roles using the cluster state API. Role information is populated during node discovery; ensure `DiscoverNodesOnStart: true` is set.

**Q: "No connections found" errors**

All policies failed to find suitable nodes. Verify that non-dedicated-cluster-manager nodes exist in the cluster. The pre-built routers include `NewRoundRobinPolicy()` as a final fallback, so this error typically indicates that all discovered nodes are unreachable.

## Policy Environment Variable Overrides

Operators can disable specific routing policies at process startup via environment variables. This is useful for debugging routing behavior, A/B testing, and emergency overrides in production.

### Environment Variables

Each policy type has a corresponding variable:

| Variable                                 | Policy Type            |
| ---------------------------------------- | ---------------------- |
| `OPENSEARCH_GO_POLICY_CHAIN`             | PolicyChain            |
| `OPENSEARCH_GO_POLICY_MUX`               | MuxPolicy              |
| `OPENSEARCH_GO_POLICY_IFENABLED`         | IfEnabledPolicy        |
| `OPENSEARCH_GO_POLICY_AFFINITY`          | affinityPolicyWrapper  |
| `OPENSEARCH_GO_POLICY_ROLE`              | RolePolicy             |
| `OPENSEARCH_GO_POLICY_ROUNDROBIN`        | RoundRobinPolicy       |
| `OPENSEARCH_GO_POLICY_COORDINATOR`       | CoordinatorPolicy      |
| `OPENSEARCH_GO_POLICY_NULL`              | NullPolicy             |
| `OPENSEARCH_GO_POLICY_INDEX_AFFINITY`    | IndexAffinityPolicy    |
| `OPENSEARCH_GO_POLICY_DOCUMENT_AFFINITY` | DocumentAffinityPolicy |

### Value Format

**Disable all instances of a type:**

```bash
# Disable all RolePolicy instances (falls through to round-robin)
OPENSEARCH_GO_POLICY_ROLE=false

# Disable affinity routing (falls back to plain role-based)
OPENSEARCH_GO_POLICY_AFFINITY=false
```

**Disable a specific instance by path:**

```bash
# Disable only the first role policy under the mux
OPENSEARCH_GO_POLICY_ROLE=chain[0].ifenabled[0].chain[0].mux[0].role[0]=false
```

**Multiple matchers (comma-separated):**

```bash
# Disable two specific role policies
OPENSEARCH_GO_POLICY_ROLE=chain[0].mux[0].role[0]=false,chain[0].mux[0].role[1]=false
```

**Regex path matching:**

```bash
# Disable all role policies under any mux
OPENSEARCH_GO_POLICY_ROLE=.*mux.*role.*=false
```

### Path Format

Each policy in the tree gets a dot-delimited path with per-type sibling indices:

```
chain[0].ifenabled[0].chain[0].mux[0].role[0]
```

Enable `OPENSEARCH_GO_DEBUG=true` to see policy paths and override actions logged to stderr.

### Value Parsing Priority

1. **Bool**: `strconv.ParseBool(value)` -- if the entire value is a valid bool, it applies to ALL instances. A value of `true` is a no-op (same as default).
2. **Path matchers**: Comma-separated `path=bool` items. The path portion is matched first as a regular expression, then as a string prefix.

### Behavior

When a policy is env-disabled:

- `IsEnabled()` returns `false`
- `Eval()` returns `(nil, nil)` (pass-through to next policy)
- `DiscoveryUpdate()` is a no-op on leaf policies (prevents accumulating connections)

The env override is applied once at startup, after policy configuration but before the first `DiscoveryUpdate`. It cannot be changed at runtime.

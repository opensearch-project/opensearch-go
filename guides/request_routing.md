# Request-Based Connection Routing

# Connection Routing

Request-based connection routing automatically routes operations to appropriate nodes based on request type and node roles, providing performance optimization with graceful fallback.

## Features

- **Performance**: Routes bulk operations to ingest nodes, search operations to data nodes
- **High availability**: Graceful fallback when specialized nodes are unavailable
- **Zero configuration**: Optional feature with transparent operation
- **Backward compatible**: Existing round-robin behavior preserved

## Quick Start

### Basic Setup

```go
// Default: round-robin across all nodes
client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"http://localhost:9200"},
})
```

### Enable Smart Routing

```go
import "github.com/opensearch-project/opensearch-go/v4/opensearchtransport"

// Create a router with smart routing policies
router := opensearchtransport.NewSmartRouter()

// Configure client with custom router
client, err := opensearchapi.NewClient(opensearchapi.Config{
    Client: opensearch.Config{
        Addresses: []string{"http://localhost:9200"},
        Transport: &opensearchtransport.Client{
            Router: router,
        },
    },
})
```

### Production Setup with Discovery

```go
import "github.com/opensearch-project/opensearch-go/v4/opensearchtransport"

// Create router with default routing strategy
router := opensearchtransport.NewDefaultRouter()

client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"http://localhost:9200"},

    // Enable node discovery
    DiscoverNodesOnStart:  true,
    DiscoverNodesInterval: 5 * time.Minute,

    // Configure transport with router
    Transport: &opensearchtransport.Client{
        Router: router,
    },
})
```

## Routing Architecture

### Router and Policies

The routing system uses a **chain-of-responsibility pattern**:

- **Router**: Top-level coordinator that tries policies in sequence
- **Policy**: Individual routing strategy that may match or pass to next policy
- **Fallthrough**: Policies return `(nil, nil)` when they don't match, allowing the next policy to try

### Available Policies

```go
// Role-based routing
opensearchtransport.NewRolePolicy("data", "ingest") // Nodes with specific roles

// Coordinating node routing
coordinatingPolicy, _ := opensearchtransport.NewRolePolicy(opensearchtransport.RoleCoordinatingOnly)

// Round-robin fallback
opensearchtransport.NewRoundRobinPolicy() // All available nodes

// HTTP pattern matching
opensearchtransport.NewMuxPolicy(routes) // Route based on HTTP patterns

// Conditional routing
opensearchtransport.NewIfEnabledPolicy(
    conditionFunc,
    truePolicy,
    falsePolicy,
) // Route based on request conditions

// Policy composition
opensearchtransport.NewPolicy(policies...) // Chain multiple policies

// Null policy
opensearchtransport.NewNullPolicy() // Always returns no connections
```

## Policy Examples

### Basic Role Routing

```go
// Route to data nodes, fallback to all nodes
dataPolicy, _ := opensearchtransport.NewRolePolicy("data")
router := opensearchtransport.NewRouter(
    dataPolicy,
    opensearchtransport.NewRoundRobinPolicy(),
)
```

### Advanced Multi-Role Strategy

For "intelligent" request routing, use the built-in smart router:

```go
// Recommended: Use pre-built smart router for comprehensive routing
router := opensearchtransport.NewSmartRouter()
```

This provides:

1. **Coordinating-only nodes** (if available, exclusive)
2. **HTTP pattern matching** (bulk->ingest, search->data)
3. **Round-robin fallback** (high availability)

You can also build custom strategies:

```go
// Custom strategy example
dataPolicy, _ := opensearchtransport.NewRolePolicy("data")
ingestPolicy, _ := opensearchtransport.NewRolePolicy("ingest")

// Not recommended for production
router := opensearchtransport.NewRouter(
    dataPolicy,   // Then data nodes
    ingestPolicy, // Try ingest nodes first
    opensearchtransport.NewRoundRobinPolicy(), // Finally all nodes
)
```

### Conditional Routing

```go
// Define condition function
// NOTE: This _bulk example is for illustration only. In practice, you would
// typically use policy.IsEnabled() to check if specific node types are available,
// rather than parsing request URLs.
isBulkRequest := func(ctx context.Context, req *http.Request) bool {
    return strings.Contains(req.URL.Path, "_bulk")
}

// Route based on request characteristics
dataPolicy, _ := opensearchtransport.NewRolePolicy("data")
ingestPolicy, _ := opensearchtransport.NewRolePolicy("ingest")

router := opensearchtransport.NewRouter(
    opensearchtransport.NewIfEnabledPolicy(
        isBulkRequest,
        ingestPolicy,  // Bulk -> ingest nodes
        dataPolicy,    // Other -> data nodes
    ),
    opensearchtransport.NewRoundRobinPolicy(), // Fallback
)
```

### Smart Router with HTTP Pattern Matching

The following illustrates how to build the "smart" router:

```go
import "github.com/opensearch-project/opensearch-go/v4/opensearchtransport"

// Get the default routes (bulk->ingest, search->data)
routes := opensearchtransport.NewDefaultRoutes()

// Create custom mux policy
muxPolicy := opensearchtransport.NewMuxPolicy(routes)

// Coordinating preference with smart fallback
coordinatingPolicy, _ := opensearchtransport.NewRolePolicy(opensearchtransport.RoleCoordinatingOnly)

router := opensearchtransport.NewRouter(
    opensearchtransport.NewIfEnabledPolicy(
        func(ctx context.Context, req *http.Request) bool { return coordinatingPolicy.IsEnabled() },
        coordinatingPolicy,
        opensearchtransport.NewNullPolicy(), // No fallthrough when coordinating available
    ),
    muxPolicy, // Smart HTTP-based routing
    opensearchtransport.NewRoundRobinPolicy(), // Final fallback
)
```

This provides the optimal strategy:

1. **Coordinator preference**: Uses dedicated client nodes when available (no fallthrough)
2. **Smart routing**: Routes bulk->ingest, search->data based on HTTP patterns
3. **High availability**: Round-robin ensures requests always succeed

**Alternative with round-robin when coordinators unavailable:**

```go
router := opensearchtransport.NewRouter(
    opensearchtransport.NewIfEnabledPolicy(
        func(ctx context.Context, req *http.Request) bool { return coordinatingPolicy.IsEnabled() },
        coordinatingPolicy,
        muxPolicy, // Falls through to smart routing when coordinators unavailable
    ),
    opensearchtransport.NewRoundRobinPolicy(), // Final fallback
)
```

## Pre-built Router Constructors

### NewDefaultRouter()

Simple routing with coordinating node preference:

```go
// Equivalent to:
// 1. Coordinating-only nodes (if available, no fallthrough)
// 2. Round-robin fallback
router := opensearchtransport.NewDefaultRouter()
```

### NewSmartRouter()

Role-aware request routing:

```go
// Equivalent to:
// 1. Coordinating-only nodes (if available, no fallthrough)
// 2. HTTP pattern-based routing (bulk->ingest, search->data)
// 3. Round-robin fallback
router := opensearchtransport.NewSmartRouter()
```

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

**Routing with smart router:**

```go
router := opensearchtransport.NewSmartRouter()
```

- Bulk operations -> coordinator-1 (if available) -> ingest-1 (fallback) -> round-robin
- Search operations -> coordinator-1 (if available) -> data-1 (fallback) -> round-robin
- Other operations -> coordinator-1 (if available) -> round-robin

### Single Node (Development)

```yaml
nodes:
  - name: opensearch-single
    roles: [cluster_manager, data, ingest]
```

**Routing:**

- All policies match the same node
- No performance difference vs round-robin
- Same code works in development and production

## Health Check Configuration

For details on the health check endpoint itself -- response fields, HTTP status codes, required permissions, and security configuration -- see [cluster_health_checking.md](cluster_health_checking.md).

Configure health check behavior for discovered nodes:

```go
client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"http://localhost:9200"},

    // Health check probing
    HealthCheckTimeout:    5 * time.Second, // Per-request timeout (default: 5s)
    HealthCheckMaxRetries: 6,               // Max retries (default: 6, -1 = disable)
    HealthCheckJitter:     0.1,             // Retry jitter factor (default: 0.1)
})
```

**Health check options:**

- `HealthCheckTimeout`: Individual check timeout (default: 5s, 0=default, <0=disable)
- `HealthCheckMaxRetries`: Max retries (default: 6, 0=default, <0=disable)
- `HealthCheckJitter`: Backoff jitter factor (default: 0.1, 0.0=default, <0.0=disable)

### Resurrection Timeout Tuning

When a node fails, the client uses exponential backoff with cluster-health-aware rate limiting to schedule resurrection attempts. This prevents busy-looping during outages and avoids overwhelming recovering servers with TLS handshake storms.

```go
client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"http://localhost:9200"},

    // Exponential backoff: initial * 2^(failures-1), capped at max
    ResurrectTimeoutInitial: 5 * time.Second,  // Default: 5s
    ResurrectTimeoutMax:     30 * time.Second,  // Default: 30s
    MinimumResurrectTimeout: 500 * time.Millisecond, // Default: 500ms
    JitterScale:             0.5,               // Default: 0.5
})
```

The retry interval is `max(healthTimeout, rateLimitedTimeout, minimumFloor) + jitter`:

- **All dead**: Minimum floor (500ms) -- aggressive, we need capacity back fast
- **Recovering**: Rate limit grows with live node count -- backs off to protect servers from TLS handshake pressure
- **Mostly healthy**: Capped at max (30s) -- very conservative

The capacity model values (`clientsPerServer`, `serverMaxNewConnsPerSec`) are auto-derived from the server's core count (discovered via `/_nodes/http,os`, default: 8 cores). See [connection_pool.md](connection_pool.md#capacity-model) for the derivation formulas.

For a 150-node cluster recovering from a full outage:

```
Live  Dead  Final Timeout  Behavior
----  ----  -------------  --------
  0    150  500ms          All dead: most aggressive
 10    140  2.5s           Rate limit: (10 * 8) / 32
 50    100  12.5s          Rate limit dominates
100     50  30s            Capped at max
149      1  30s            Nearly healthy: most conservative
```

### Readiness Health Checks

When cluster health checking is available (the node permits `GET /_cluster/health?local=true`), the client uses a **two-phase readiness gate** before adding a node to the ready pool:

1. **Phase 1** -- `GET /` confirms HTTP connectivity and extracts the server version.
2. **Phase 2** -- `GET /_cluster/health?local=true` checks shard initialization status.

If `initializing_shards > 0`, the node stays in the dead list and is retried on the next health check cycle. This prevents routing traffic to a node that is reachable but still absorbing shard data after a restart.

When all nodes are recovering simultaneously (cold start), requests still succeed via the zombie connection fallback -- the pool rotates through dead connections one at a time until nodes finish initializing.

### Node Stats Polling and Load Shedding

A background goroutine polls each live node's JVM heap usage and circuit breaker metrics to detect overloaded nodes and shed load away from them. **This is enabled by default** with a 30-second polling interval.

```go
client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"http://localhost:9200"},

    // Defaults (load shedding is enabled out of the box):
    // NodeStatsInterval:       30 * time.Second, // 0 = default (30s), <0 = disabled
    // OverloadedHeapThreshold: 85,               // JVM heap % (default: 85)
    // OverloadedBreakerRatio:  0.90,             // Breaker estimated/limit (default: 0.90)
})

// To disable load shedding programmatically:
client, err := opensearch.NewClient(opensearch.Config{
    Addresses:         []string{"http://localhost:9200"},
    NodeStatsInterval: -1, // Negative value disables polling
})
```

**How it works:**

The stats poller fetches `GET /_nodes/_local/stats/jvm,breaker` from each live node and evaluates overload conditions. A node is marked overloaded if **any** of these are true:

- JVM `heap_used_percent` >= `OverloadedHeapThreshold`
- Any circuit breaker's `estimated_size / limit_size` >= `OverloadedBreakerRatio`
- Any circuit breaker's `tripped` count increased since the last poll
- Cluster health status is `"red"` (reuses data from cluster health checks -- no extra HTTP call)

**Overloaded nodes are demoted from the ready list to the dead list.** This is the same mechanism used for failed nodes, but with a key difference:

- `dead + isOverloaded` = overload-demoted (stats poller manages lifecycle)
- `dead + !isOverloaded` = real failure (resurrection scheduler manages lifecycle)

The stats poller promotes nodes back to ready when metrics improve. The resurrection scheduler skips overloaded connections entirely -- it only handles real failures.

**When all nodes are overloaded:**

All nodes move to the dead list. Requests continue via `tryZombieWithLock()`, which rotates through dead connections one at a time. This provides natural backpressure -- the cluster is not abandoned, but traffic is reduced to a trickle until load drops.

**Configuration options:**

- `NodeStatsInterval`: Polling interval. `0` = auto-derive from cluster size (`clamp(liveNodes * clientsPerServer / healthCheckRate, 5s, 30s)`). `>0` = explicit fixed interval. `<0` = disabled.
- `OverloadedHeapThreshold`: JVM heap percent threshold. Default: `85`. Range: `0`-`100`. Set to `100` to effectively disable heap-based detection.
- `OverloadedBreakerRatio`: Breaker size ratio threshold. Default: `0.90`. Range: `0.0`-`1.0`.

**Environment variable overrides:**

Operators can tune load shedding at deployment time without recompiling:

| Variable | Format | Default | Description |
| --- | --- | --- | --- |
| `OPENSEARCH_GO_NODE_STATS_INTERVAL` | Duration or seconds | auto (5s-30s) | Stats polling interval. Accepts `time.ParseDuration` format (e.g., `"30s"`, `"1m"`) or an integer number of seconds. `0` or unset = auto-derive from cluster size. Negative value = disabled. |
| `OPENSEARCH_GO_OVERLOADED_HEAP_THRESHOLD` | Integer (0-100) | `85` | JVM `heap_used_percent` threshold. Set to `100` to effectively disable heap-based overload detection. |
| `OPENSEARCH_GO_OVERLOADED_BREAKER_RATIO` | Float (0.0-1.0) | `0.90` | Circuit breaker `estimated_size / limit_size` threshold. Set to `1.0` to effectively disable breaker-based overload detection. |

Environment variables take precedence over programmatic `Config` values. Examples:

```bash
# Use 10-second polling interval
OPENSEARCH_GO_NODE_STATS_INTERVAL=10s ./myapp

# Disable load shedding entirely
OPENSEARCH_GO_NODE_STATS_INTERVAL=-1 ./myapp

# Raise heap threshold to 95% (more tolerant)
OPENSEARCH_GO_OVERLOADED_HEAP_THRESHOLD=95 ./myapp

# Disable heap-based detection (breaker and cluster-health checks still active)
OPENSEARCH_GO_OVERLOADED_HEAP_THRESHOLD=100 ./myapp

# Raise breaker ratio threshold to 0.95 (more tolerant)
OPENSEARCH_GO_OVERLOADED_BREAKER_RATIO=0.95 ./myapp
```

## Migration

### From Round-Robin

```go
// Before: Default round-robin behavior
client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"http://localhost:9200"},
})

// After: No change needed - router is optional
// Add router only when you need smart routing
```

### Adding Smart Routing

Since routing is implemented at the transport layer, advanced routing requires transport configuration:

```go
// Create transport with router
transport, err := opensearchtransport.New(opensearchtransport.Config{
    URLs: []*url.URL{...},
    Router: opensearchtransport.NewSmartRouter(),
})

// Use transport with client
client := &opensearch.Client{Transport: transport}
```

## Troubleshooting

**Q: How do I verify routing is working?**

- Enable debug logging to see which connections are selected by policies

**Q: Can I disable request routing?**

- Yes, omit `Router` configuration to use standard connection pool behavior

**Q: Why don't my role policies match?**

- Check that nodes have the expected roles using the cluster state API
- Ensure node discovery is enabled to populate role information

**Q: "No connections found" errors**

- This indicates all policies failed to find suitable nodes (are coordinator nodes in the cluster but unavailable?)
- Add `NewRoundRobinPolicy()` as final fallback policy

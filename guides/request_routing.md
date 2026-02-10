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

Configure health check behavior for discovered nodes:

```go
client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"http://localhost:9200"},

    // Health check configuration (opensearchtransport level)
    // Note: These are passed through opensearch.Config to opensearchtransport.Config
})
```

**Health check options:**

- `DiscoveryHealthCheckRetries`: Retries during discovery (default: 3)
- `HealthCheckTimeout`: Individual check timeout (default: 5s, 0=default, <0=disable)
- `HealthCheckMaxRetries`: Max retries (default: 6, 0=default, <0=disable)
- `HealthCheckJitter`: Backoff jitter factor (default: 0.1, 0.0=default, <0.0=disable)

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

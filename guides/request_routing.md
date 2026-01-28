# Request-Based Connection Routing

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

// Recommended: intelligent request routing
client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"http://localhost:9200"},
    Selector: opensearchtransport.NewDefaultSelector(),
})
```

### Custom Routing

```go
// Advanced: compose multiple routing strategies
client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"http://localhost:9200"},
    Selector: opensearchtransport.NewChainSelector(
        opensearchtransport.WithSelector(customSelector),
        opensearchtransport.WithSelector(opensearchtransport.NewDefaultSelector()),
    ),
})
```

## Routing Patterns

**Bulk operations** → Ingest-capable nodes

- `POST /_bulk`, `PUT /_bulk`
- `POST /{index}/_bulk`, `PUT /{index}/_bulk`
- `POST /_bulk/stream`, `PUT /_bulk/stream` (OpenSearch 3.0+)

**Ingest pipelines** → Ingest-capable nodes

- `GET/PUT/DELETE /_ingest/pipeline/{id}`
- `POST /_ingest/pipeline/_simulate`

**Search operations** → Data nodes

- `GET/POST /_search`, `GET/POST /_msearch`
- `GET/POST /{index}/_search`
- `GET/POST /_count`, `POST /_delete_by_query`

**Document retrieval** → Data nodes

- `GET/HEAD /{index}/_doc/{id}`
- `GET /_mget`, `POST /_mget`

**All other operations** → Round-robin fallback

## Fallback Behavior

Operations never fail due to missing specialized nodes. When appropriate node types are unavailable, the system gracefully falls back to round-robin selection across all healthy nodes.

## Selector Types

### Available Selectors

```go
// Smart routing (recommended)
opensearchtransport.NewDefaultSelector()

// Round-robin load balancing
opensearchtransport.NewRoundRobinSelector()

// Role-based filtering
opensearchtransport.NewRoleBasedSelector(
    opensearchtransport.WithRequiredRoles("data", "ingest"),
)

// HTTP pattern matching
opensearchtransport.NewSelectorMux(routes)

// Chain multiple selectors
opensearchtransport.NewChainSelector(
    opensearchtransport.WithSelector(selector1),
    opensearchtransport.WithSelector(selector2),
)
```

### Role-Based Options

- `WithRequiredRoles(roles...)` - Require specific node roles
- `WithExcludedRoles(roles...)` - Exclude specific node roles
- `WithStrictMode()` - Disable fallback when no matching roles
- `WithFallback(selector)` - Custom fallback selector

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
```

**Routing:**

- Bulk operations → `ingest-1`
- Search operations → `data-1`
- Other operations → Round-robin across `ingest-1`, `data-1`

### Single Node (Development)

```yaml
nodes:
  - name: opensearch-single
    roles: [cluster_manager, data, ingest]
```

**Routing:**

- All operations → `opensearch-single`
- No performance difference vs round-robin
- Same code works in development and production

## Migration

### From Round-Robin

```go
// Before
client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"http://localhost:9200"},
})

// After
client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"http://localhost:9200"},
    Selector: opensearchtransport.NewDefaultSelector(),
})
```

### Production Setup

```go
client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"http://localhost:9200"},

    // Node discovery
    DiscoverNodesOnStart:  true,
    DiscoverNodesInterval: 5 * time.Minute,

    // Smart routing
    Selector: opensearchtransport.NewDefaultSelector(),
})
```

## Troubleshooting

**Q: Bulk operations seem slow**

- Check cluster has ingest-capable nodes. Operations fall back to data nodes if not.

**Q: "No connections found" errors**

- This indicates network/authentication issues, not routing failures.

**Q: How to verify routing is working?**

- Enable debug logging and monitor which nodes receive requests.

**Q: Can I disable request routing?**

- Yes, omit `Selector` or use `NewRoundRobinSelector()` directly.

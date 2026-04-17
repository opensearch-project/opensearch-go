# Node Discovery and Role Management

This guide covers OpenSearch node discovery and role-based node management in the Go client.

## Basic Node Discovery

### Enabling Node Discovery

```go
client, err := opensearch.NewClient(opensearch.Config{
    Addresses:             []string{"https://localhost:9200"},
    DiscoverNodesOnStart:  true,
    DiscoverNodesInterval: 5 * time.Minute,
})
```

When discovery is enabled, the client calls `/_nodes/http` to retrieve the full node list with roles and HTTP publish addresses. Hardware info (`allocated_processors`) is obtained separately: when a new node appears with `lcNeedsHardware` set, or when a node fails and may have been replaced with a different instance type, the next health check for that connection substitutes `/_nodes/_local/http,os` to discover the node's core count. This avoids fetching OS info on every discovery cycle; the info is requested only on transitions where it may have changed.

### Manual Node Discovery

```go
err := client.DiscoverNodes()
if err != nil {
    log.Printf("Error discovering nodes: %s", err)
}
```

## Node Roles

OpenSearch nodes have roles that determine their capabilities. The Go client provides constants for these roles:

```go
import "github.com/opensearch-project/opensearch-go/v4/opensearchtransport"

opensearchtransport.RoleData                // Data nodes: store documents, handle indexing and search (1.0+)
opensearchtransport.RoleIngest              // Ingest nodes: pre-process documents via pipelines (1.0+)
opensearchtransport.RoleClusterManager      // Cluster manager nodes: manage cluster state (1.0+)
opensearchtransport.RoleSearch              // Search nodes: dedicated search replicas (3.0+)
opensearchtransport.RoleWarm                // Warm nodes: searchable snapshots (2.4+)
opensearchtransport.RoleRemoteClusterClient // Cross-cluster client capability (1.0+)
opensearchtransport.RoleML                  // Machine learning tasks (ML Commons plugin)
opensearchtransport.RoleCoordinatingOnly    // Derived: no explicit roles, acts as coordinator
opensearchtransport.RoleMaster              // Deprecated: use RoleClusterManager
```

### Role Descriptions

**Data** (`data`): Store documents in local shards. Handle indexing, search, and aggregation operations. The primary workhorse role present in most clusters.

**Ingest** (`ingest`): Pre-process documents through ingest pipelines before indexing. Separating pipeline CPU load from search and indexing workloads can improve overall cluster stability.

**Cluster Manager** (`cluster_manager`): Manage cluster state, index creation and deletion, shard allocation, and node health. Dedicated cluster-manager nodes improve cluster stability under load.

**Search** (`search`): Host dedicated search replicas. Added in OpenSearch 3.0 to allow separation of search workloads from indexing workloads. Search nodes host search-only replicas of shards and do not participate in indexing. This is a distinct role from data nodes: search nodes receive replicated data but never handle write operations. The server enforces that search nodes cannot hold other data-related roles.

**Warm** (`warm`): Provide access to warm indexes and searchable snapshots. Added in OpenSearch 2.4. In OpenSearch 3.0+, the warm role replaced the earlier use of the search role for searchable snapshot functionality. Warm nodes store snapshot data on local or remote storage and serve read requests from it.

**Coordinating-only** (`coordinating_only`): Nodes with no explicit roles (`node.roles: []`). These nodes accept client requests, route them to the appropriate data or search nodes, and aggregate results. They do not store data or manage cluster state. Ideal for absorbing coordinator overhead in large clusters.

**Remote Cluster Client** (`remote_cluster_client`): A capability role that enables outbound connections to remote clusters for cross-cluster search and replication. This role does not affect request routing and is ignored during node filtering.

### OpenSearch 3.0 Role Changes

OpenSearch 3.0 introduced two significant role changes:

1. **Search role** (`search`): New dedicated role for hosting search replicas. Separates search traffic from indexing on data nodes. Not to be confused with the warm role.

2. **Warm role for searchable snapshots**: In OpenSearch 2.x, searchable snapshots used the `search` role. In 3.0+, this functionality moved to the `warm` role. Nodes running searchable snapshots must use `warm`, not `search`.

These are distinct roles serving different purposes:

| Role | Purpose | Data Source | Write Traffic |
| --- | --- | --- | --- |
| `search` | Dedicated search replicas | Replicated from primary shards | None (read-only replicas) |
| `warm` | Searchable snapshots | Snapshot storage (local/remote) | None (read-only) |
| `data` | General purpose | Local shards (primary + replica) | Yes (indexing + search) |

## Cluster Manager Filtering

By default, the client excludes dedicated cluster manager nodes from request routing. These nodes are EXCLUDED:

- Nodes with only `cluster_manager` role
- Nodes with only `master` role (deprecated)
- Nodes with `cluster_manager` + `remote_cluster_client` roles only

These nodes are INCLUDED (they have data-serving capabilities):

- Cluster manager + data nodes
- Cluster manager + ingest nodes
- Cluster manager + warm nodes
- Pure data, ingest, warm, or search nodes
- Pure `remote_cluster_client` nodes (effectively coordinating-only)

```go
client, err := opensearch.NewClient(opensearch.Config{
    Addresses:            []string{"https://localhost:9200"},
    DiscoverNodesOnStart: true,

    // Default: false (excludes dedicated cluster managers)
    IncludeDedicatedClusterManagers: false,
})
```

## Role Validation

The client validates discovered node roles and logs warnings for deprecated or conflicting configurations.

### Deprecated Roles

The client logs warnings for deprecated role usage:

- **`master` role**: Deprecated in favor of `cluster_manager`. Functionally identical, but `cluster_manager` is the preferred name.
- **`search` role for searchable snapshots**: In OpenSearch 3.0+, searchable snapshots require the `warm` role instead.

### Conflicting Roles (Server-Enforced)

OpenSearch enforces role constraints server-side. The following combinations produce validation errors when configuring the server:

- `master` + `cluster_manager` on the same node (contradictory names for the same role)
- `search` + other data-related roles (search nodes are search-only by design)
- `warm` + `search` on the same node in OpenSearch 3.0+ (distinct purposes)

The client detects these conflicts during discovery and logs errors to help diagnose misconfigured clusters.

## Migration Guide

### From Master to Cluster Manager

```yaml
# Deprecated
node.roles: ["master", "data"]

# Recommended
node.roles: ["cluster_manager", "data"]
```

### From Search to Warm for Searchable Snapshots (OpenSearch 3.0+)

```yaml
# Deprecated for searchable snapshots in OpenSearch 3.0+
node.roles: ["search"]
node.search.cache.size: 50gb

# Recommended for searchable snapshots
node.roles: ["warm"]
node.search.cache.size: 50gb
```

Note: The `search` role in 3.0+ is _not_ deprecated; it has a new purpose (dedicated search replicas). Only its use for searchable snapshots is deprecated in favor of `warm`.

### Role Separation Best Practices

For production clusters, separate roles for workload isolation:

```yaml
# Dedicated cluster manager (3 nodes for quorum)
node.roles: ["cluster_manager"]

# Data + ingest (combined is common for smaller clusters)
node.roles: ["data", "ingest"]

# Dedicated search replicas (OpenSearch 3.0+)
node.roles: ["search"]

# Searchable snapshots (OpenSearch 2.4+)
node.roles: ["warm"]
node.search.cache.size: 50gb

# Coordinating-only (absorbs coordinator overhead)
node.roles: []
```

## Troubleshooting

### All nodes excluded from routing

Check that non-dedicated-cluster-manager nodes exist. If the cluster contains only `cluster_manager`-role nodes plus the seed addresses, the client will have no nodes to route to after discovery.

### Role validation errors in logs

Remove conflicting role combinations from the server's `opensearch.yml`. Common conflicts: `master` + `cluster_manager`, `search` + `data`, `warm` + `search` (3.0+).

### Discovery failures

- Verify network connectivity to cluster nodes on their HTTP publish addresses, not only the seed addresses.
- Check authentication credentials: discovery calls `/_nodes/http,os`, which requires the `cluster:monitor/nodes` permission.
- Ensure the seed addresses are reachable and point to nodes in the target cluster.

## Example: Complete Setup

```go
package main

import (
    "log"
    "time"

    "github.com/opensearch-project/opensearch-go/v4"
    "github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
)

func main() {
    router := opensearchtransport.NewDefaultRouter()

    client, err := opensearch.NewClient(opensearch.Config{
        Addresses:             []string{"https://localhost:9200"},
        Username:              "admin",
        Password:              "changeme",
        DiscoverNodesOnStart:  true,
        DiscoverNodesInterval: 5 * time.Minute,
        Router:                router,
    })
    if err != nil {
        log.Fatalf("Error creating client: %s", err)
    }

    // Client will automatically:
    // 1. Discover cluster nodes on startup
    // 2. Validate node roles for compatibility
    // 3. Route requests to appropriate nodes based on roles + connection scoring
    // 4. Refresh node list every 5 minutes
    // 5. Log deprecation warnings for old role names
    _ = client
}
```

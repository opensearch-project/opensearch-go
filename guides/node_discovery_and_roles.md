# Node Discovery and Role Management

This guide covers OpenSearch node discovery functionality and role-based node selection in the Go client.

## Overview

The OpenSearch Go client can automatically discover nodes in your cluster and select appropriate nodes for routing requests based on their roles. This feature helps ensure optimal performance and follows OpenSearch best practices.

## Basic Node Discovery

### Enabling Node Discovery

```go
client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"http://localhost:9200"},
    // Discover nodes when the client starts
    DiscoverNodesOnStart: true,
    // Periodically refresh the node list every 5 minutes
    DiscoverNodesInterval: 5 * time.Minute,
})
```

### Manual Node Discovery

You can also trigger node discovery manually:

```go
err := client.DiscoverNodes()
if err != nil {
    log.Printf("Error discovering nodes: %s", err)
}
```

## Node Roles

OpenSearch nodes can have various roles that determine their capabilities. The Go client provides constants for these roles:

```go
import "github.com/opensearch-project/opensearch-go/v4/opensearchtransport"

// Available role constants
opensearchtransport.RoleData                // Data nodes store documents and handle search
opensearchtransport.RoleIngest              // Ingest nodes process documents before indexing
opensearchtransport.RoleClusterManager      // Cluster manager nodes manage cluster state
opensearchtransport.RoleMaster              // Deprecated: use RoleClusterManager instead
opensearchtransport.RoleSearch              // Deprecated in 3.0+: use RoleWarm for searchable snapshots
opensearchtransport.RoleWarm                // Warm nodes for searchable snapshots (OpenSearch 3.0+)
opensearchtransport.RoleRemoteClusterClient // Enables cross-cluster connections
```

### OpenSearch 3.0 Role Changes

**Important**: As of OpenSearch 3.0, there have been significant changes to node roles:

- **Searchable Snapshots**: Nodes that use the searchable snapshots feature must have the `warm` node role instead of the `search` role.
- **Role Migration**: The `search` role is deprecated for searchable snapshots functionality in OpenSearch 3.0+.

## Role-Based Node Selection

### Dedicated Cluster Manager Nodes

By default, the client includes all discovered nodes in request routing. However, you can enable Java client compatible behavior to exclude dedicated cluster manager nodes from receiving client requests:

```go
client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"http://localhost:9200"},

    // Enable node discovery
    DiscoverNodesOnStart: true,
    DiscoverNodesInterval: 5 * time.Minute,

    // Enable Java client compatible behavior
    IncludeDedicatedClusterManagers: false, // Default: excludes dedicated cluster managers
})
```

When `IncludeDedicatedClusterManagers` is disabled (default), these nodes will be EXCLUDED from request routing:

- Nodes with only "cluster_manager" role
- Nodes with only "master" role (deprecated)
- Nodes with "cluster_manager" + "remote_cluster_client" roles only

These nodes will be INCLUDED (even with IncludeDedicatedClusterManagers disabled):

- Cluster manager + data nodes
- Cluster manager + ingest nodes
- Cluster manager + warm nodes (OpenSearch 3.0+ for searchable snapshots)
- Pure data nodes
- Pure ingest nodes
- Pure warm nodes
- Search nodes (backward compatibility)
- Pure remote_cluster_client nodes (coordinating nodes with cross-cluster capability)

### Remote Cluster Client Role

The `remote_cluster_client` role is a **capability role** that enables cross-cluster connections but does not affect node selection for request routing. Nodes with this role can make outbound connections to remote clusters for cross-cluster search and replication operations.

```go
// Valid: Pure remote cluster client (treated as coordinating node)
roles := []string{opensearchtransport.RoleRemoteClusterClient} // INCLUDED - effectively coordinating-only

// Valid: Combined with other roles
roles := []string{opensearchtransport.RoleData, opensearchtransport.RoleRemoteClusterClient} // INCLUDED - data node with capability

// Filtered: Cluster manager + remote cluster client only (follows cluster manager filtering)
roles := []string{opensearchtransport.RoleClusterManager, opensearchtransport.RoleRemoteClusterClient} // EXCLUDED - dedicated cluster manager
```

Since `remote_cluster_client` is a capability role, it is ignored during node filtering. This matches OpenSearch server behavior where the role enables outbound connections but doesn't determine inbound request eligibility.

### Warm Nodes for Searchable Snapshots (OpenSearch 3.0+)

Warm nodes are now the preferred method for searchable snapshots:

```go
// Recommended for OpenSearch 3.0+: Warm node for searchable snapshots
roles := []string{opensearchtransport.RoleWarm}

// Also valid: Warm node combined with data
roles := []string{opensearchtransport.RoleWarm, opensearchtransport.RoleData}

// DEPRECATED in OpenSearch 3.0+: Search role for searchable snapshots
roles := []string{opensearchtransport.RoleSearch} // Will log deprecation warning
```

### Search Node Constraints

Search nodes have special constraints for backward compatibility:

```go
// Valid: Search-only node (backward compatibility)
roles := []string{opensearchtransport.RoleSearch}

// INVALID: Search nodes cannot have other roles
roles := []string{opensearchtransport.RoleSearch, opensearchtransport.RoleData} // Will cause validation error

// INVALID: Cannot mix warm and search roles (OpenSearch 3.0+)
roles := []string{opensearchtransport.RoleWarm, opensearchtransport.RoleSearch} // Will cause validation error
```

## Role Validation

The client validates node roles for compatibility and logs warnings for deprecated configurations:

### Deprecated Roles

```go
// Master role deprecation warning:
// "DEPRECATION WARNING: Node [node-1] uses deprecated 'master' role.
//  Please use 'cluster_manager' role instead to promote inclusive language"

// Search role deprecation warning (OpenSearch 3.0+):
// "DEPRECATION WARNING: Node [node-1] uses 'search' role. As of OpenSearch 3.0,
//  searchable snapshots functionality requires 'warm' role instead.
//  Consider migrating to 'warm' role for future compatibility"
```

### Conflicting Roles

```go
// This configuration will cause an error:
// "node [node-1] has conflicting roles ["master", "cluster_manager"] -
//  these cannot be assigned together"

// This configuration will cause an error (OpenSearch 3.0+):
// "node [node-1] cannot have both "warm" and "search" roles -
//  use "warm" for searchable snapshots in OpenSearch 3.0+"
```

## OpenSearch 3.X Compatibility

The enhanced role validation ensures compatibility with OpenSearch 3.X best practices:

- **Prevents anti-patterns**: Master + data role combinations that can impact cluster stability
- **Enforces role separation**: Encourages dedicated cluster manager nodes
- **Searchable snapshots migration**: Guides users from deprecated `search` role to `warm` role
- **Future compatibility**: Graceful handling of deprecated roles with warnings

## Migration Guide

### From Master to Cluster Manager

If you're using deprecated "master" roles, migrate to "cluster_manager":

```yaml
# Old configuration (deprecated)
node.roles: ["master", "data"]

# New configuration (recommended)
node.roles: ["cluster_manager", "data"]
```

### From Search to Warm (OpenSearch 3.0+)

For searchable snapshots, migrate from "search" to "warm":

```yaml
# Old configuration (deprecated in OpenSearch 3.0+)
node.roles: ["search"]
node.search.cache.size: 50gb

# New configuration (OpenSearch 3.0+)
node.roles: ["warm"]
node.search.cache.size: 50gb
```

### Role Separation Best Practices

For production clusters, consider separating roles:

```yaml
# Dedicated cluster manager nodes
node.roles: ["cluster_manager"]

# Dedicated data nodes
node.roles: ["data", "ingest"]

# Dedicated warm nodes for searchable snapshots (OpenSearch 3.0+)
node.roles: ["warm"]
node.search.cache.size: 50gb
```

## Performance Optimizations

The client includes several performance improvements for handling node roles:

- **Fast role lookups**: Role checking is optimized for speed
- **Efficient validation**: Role compatibility is checked only when nodes are discovered
- **Minimal overhead**: Role information is processed once per node during discovery

## Troubleshooting

### Common Issues

1. **All nodes excluded from routing**
   - Check that you have non-dedicated cluster manager nodes
   - Ensure data/ingest nodes are available

2. **Role validation errors**
   - Remove conflicting master+cluster_manager combinations
   - Ensure search nodes don't have other roles

3. **Discovery failures**
   - Verify network connectivity to cluster nodes
   - Check authentication credentials
   - Review cluster node configuration

### Debug Logging

Enable debug logging to see node discovery details:

```go
import "github.com/opensearch-project/opensearch-go/v4/opensearchtransport"

// The client will log discovered nodes and role validation results
// when debug logging is enabled in your application
```

## Example: Complete Setup

```go
package main

import (
    "log"
    "net/http"
    "time"

    "github.com/opensearch-project/opensearch-go/v4"
    "github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
)

func main() {
    client, err := opensearch.NewClient(opensearch.Config{
        Addresses: []string{"http://localhost:9200"},

        // Enable automatic node discovery
        DiscoverNodesOnStart:  true,
        DiscoverNodesInterval: 5 * time.Minute,

        // Optional: Custom HTTP transport for additional configuration
        Transport: &http.Transport{
            MaxIdleConnsPerHost:   10,
            ResponseHeaderTimeout: time.Second,
        },
    })
    if err != nil {
        log.Fatalf("Error creating client: %s", err)
    }

    // Client will automatically:
    // 1. Discover cluster nodes on startup
    // 2. Validate node roles for compatibility
    // 3. Route requests only to appropriate nodes
    // 4. Refresh node list periodically
    // 5. Log deprecation warnings for old role names
}
```


# Cluster Health Checking

The Go client performs active health checks using a two-phase approach: it begins with `GET /` (which requires no special permissions) and asynchronously probes `GET /_cluster/health?local=true` to determine whether richer health data is available. This guide covers how capability detection works, the permissions required when the OpenSearch Security plugin is enabled, and how to interpret the response.

For health check **timing and backoff**, see [retry_backoff.md](retry_backoff.md). For health check **routing** (which node receives the probe), see [routing.md](routing.md).

## Capability Detection Lifecycle

The `/_cluster/health?local=true` endpoint requires the `cluster:monitor/health` permission, which many service accounts lack. Rather than requiring users to configure permissions before the client can start, the client detects the capability automatically:

```
                  ┌──────────────┐
      ┌──────────►│   Pending    │◄─── connection created (initial state)
      │           └──────┬───────┘
      │                  │ first successful GET /
      │                  ▼
      │           ┌─────────────────────────────────────────┐
      │           │ async probe /_cluster/health?local=true │
      │           └──────┬────────┬─────────────────────────┘
      │              200 │        │ 401/403
      │                  ▼        ▼
      │         ┌──────────┐  ┌─────────────┐
      │         │Available │  │ Unavailable │
      │         └──────────┘  └──────┬──────┘
      │                              │ after MaxRetryClusterHealth
      │                              │ (default 4h, with jitter)
      └──────────────────────────────┘
```

1. **Pending** (initial state): The connection has not been probed. Health checks use `GET /`, which validates basic connectivity and parses the OpenSearch version from the response.

2. **Probe**: After a successful `GET /`, the client launches an asynchronous `GET /_cluster/health?local=true` in the background. This does **not** block the health check; the connection is already marked healthy and serving traffic.

3. **Available**: The probe returned 200. Subsequent health checks use `/_cluster/health?local=true` exclusively, and the parsed `ClusterHealthLocal` data is stored on the `Connection` for routing decisions.

4. **Unavailable**: The probe returned 401 or 403 (missing `cluster:monitor/health` permission). The client records the timestamp and continues using `GET /`. After `MaxRetryClusterHealth` elapses (default 4h, with jitter from `HealthCheckJitter`), the client re-probes in case the permission was granted.

5. **Transient errors** (5xx, network timeout, etc.): The state remains at Pending and the probe is retried on the next health check cycle. No timestamp is recorded, so transient failures do not lock out retries.

### Runtime Fallback

If the cluster health endpoint was previously working (Available) and later returns 401 or 403 (for example, the permission was revoked), the client:

- Falls back to `GET /` for the current health check (so it still succeeds).
- Resets the state to Pending.
- Zeroes out the stale `ClusterHealthLocal` data on the connection.
- Re-probes on the next health check cycle.

### Periodic Refresh of Live Connections

The initial probe stores `ClusterHealthLocal` on the connection, but that data becomes stale if the connection stays ready for hours. The client runs a background goroutine that periodically refreshes `ClusterHealthLocal` on every ready connection that supports cluster health. This keeps health data current for load-shedding and routing decisions.

**Refresh interval formula:**

```
refreshInterval = clamp(liveNodes * clientsPerServer / healthCheckRate, 5s, 5min)
```

The formula spreads polls across all clients sharing a server. As the cluster grows, each client polls less frequently to remain within the per-server budget. After each cycle, the interval is recalculated; if node discovery changes the ready count, the ticker adjusts automatically.

The capacity model values (`clientsPerServer`, `healthCheckRate`) are auto-derived from the server's core count (discovered via `/_nodes/http,os`, default: 8 cores).

**Example intervals** (default 8-core servers: `clientsPerServer=8`, `healthCheckRate=0.8`):

| Live Nodes | Raw Interval | Clamped               |
| ---------- | ------------ | --------------------- |
| 1          | 10s          | skipped (single-node) |
| 3          | 30s          | 30s                   |
| 10         | 100s         | 100s                  |
| 30         | 5min         | 5min (capped)         |
| 100        | 16.7min      | 5min (capped)         |

**Single-node clusters** skip periodic refresh entirely; there is no routing benefit when only one node exists.

**Status code handling** during refresh mirrors the capability detection lifecycle:

- **200**: Parse and store updated `ClusterHealthLocal`.
- **401/403**: Permission revoked. Resets state to Pending and zeroes out stale data. The connection falls back to `GET /` health checks and re-probes via `MaxRetryClusterHealth`.
- **5xx / network error**: Skipped silently; the next cycle retries.

### Configuration

| Config Field | Default | Description |
| --- | --- | --- |
| `MaxRetryClusterHealth` | `4h` | Retry interval for re-probing unavailable nodes. `0` = use default, `<0` = disable probing entirely. |
| `HealthCheckRequestModifier` | `nil` | Callback applied to every health check request (both `GET /` and `/_cluster/health`). Use this to inject custom auth headers. |

```go
client, err := opensearchapi.NewClient(opensearchapi.Config{
    Client: opensearch.Config{
        Addresses: []string{"https://localhost:9200"},

        // Retry the cluster health probe every 2 hours instead of 4
        MaxRetryClusterHealth: 2 * time.Hour,

        // Inject a custom header on health check requests
        HealthCheckRequestModifier: func(req *http.Request) {
            req.Header.Set("X-Custom-Auth", "my-token")
        },
    },
})
```

## How the Health Check Works

The client issues `GET /_cluster/health?local=true` against individual nodes (once capability detection has confirmed availability; see above). The `local=true` parameter causes the request to be served from the connected node's local cluster state snapshot rather than requiring a round-trip to the cluster-manager node, making the request fast, lightweight, and safe to call even when the cluster manager is unreachable.

The response is a JSON object:

```json
{
  "cluster_name": "my-cluster",
  "status": "green",
  "timed_out": false,
  "number_of_nodes": 3,
  "number_of_data_nodes": 3,
  "discovered_cluster_manager": true,
  "active_primary_shards": 5,
  "active_shards": 10,
  "relocating_shards": 0,
  "initializing_shards": 0,
  "unassigned_shards": 0,
  "delayed_unassigned_shards": 0,
  "number_of_pending_tasks": 0,
  "number_of_in_flight_fetch": 0,
  "task_max_waiting_in_queue_millis": 0,
  "active_shards_percent_as_number": 100.0
}
```

The `status` field reports overall cluster health:

| Status | Meaning |
| --- | --- |
| `green` | All primary and replica shards are assigned. |
| `yellow` | All primary shards are assigned, but some replicas are not. The cluster is functional but not fully redundant. |
| `red` | Some primary shards are unassigned. Data loss or unavailability may be occurring. |

A single-node development cluster will always report `yellow` because there is no second node to host replica shards. This is expected and does not indicate a problem.

### Why Not `wait_for_status`?

The `wait_for_status` parameter (for example, `?wait_for_status=green&timeout=5s`) blocks the HTTP connection server-side until the cluster reaches the requested status or the timeout expires. This is designed for orchestration scenarios such as waiting for a cluster to become ready after a rolling restart. It is not suitable for periodic health probes because:

- It holds a connection open for the duration of the wait, consuming both client and server resources on repeated blocked requests.
- It imposes a rigid definition of "healthy" at the HTTP layer. A `yellow` cluster is fully functional for most workloads, but `wait_for_status=green` would cause the probe to time out on every single-node cluster.
- The client (or its caller) should decide what cluster status is acceptable, not an HTTP query parameter.

The client uses poll-and-parse instead: issue the request, read the `status` field, and let the caller decide what action to take.

## HTTP Response Status Codes

| HTTP Status | Meaning |
| --- | --- |
| **200** | Success. Parse the `status` and `timed_out` fields from the response body. |
| **400** | Malformed request (invalid query parameters). |
| **401** | Authentication failure: credentials are missing or invalid. |
| **403** | Authorization failure: the user is authenticated but lacks the required permission. |
| **408** | The request timed out. Only occurs when `wait_for_*` parameters are used. |
| **429** | The node's thread pool rejected the request (server-side backpressure). |
| **500** | Unexpected server error. |
| **503** | The node is not ready to accept requests (e.g., still starting up). |

### Distinguishing Auth Errors from Cluster Failures

When interpreting health check results, it is important to distinguish between:

- **Connectivity errors** (connection refused, TCP timeout): The node may be down. The client should mark the connection as dead and schedule resurrection per the backoff policy described in [retry_backoff.md](retry_backoff.md).
- **401/403 responses**: The node is reachable and responsive, but the client's credentials are incorrect or insufficient. The client should **not** mark the connection as dead; the cluster is healthy, and the problem is client configuration. These errors should be surfaced clearly so the operator can correct credentials or role mappings.

## Required Permissions

Without the OpenSearch Security plugin, all requests are permitted and no authentication is required.

When the Security plugin is enabled, the health check endpoint requires the `cluster:monitor/health` transport action privilege. This is a read-only monitoring action. The `local=true` parameter does not alter the permission check; the same privilege is required regardless.

### Client Authentication

The client must present valid credentials on every request, including health checks. OpenSearch supports three authentication mechanisms:

**HTTP Basic Auth** (most common):

```go
client, err := opensearchapi.NewClient(opensearchapi.Config{
    Client: opensearch.Config{
        Addresses: []string{"https://localhost:9200"},
        Username:  "health-check-user",
        Password:  "changeme",
    },
})
```

**Bearer Token (JWT):**

```go
client, err := opensearchapi.NewClient(opensearchapi.Config{
    Client: opensearch.Config{
        Addresses: []string{"https://localhost:9200"},
        Header: http.Header{
            "Authorization": []string{"Bearer <token>"},
        },
    },
})
```

**TLS Client Certificates (mutual TLS):**

> **Important:** When constructing a custom `http.Transport`, you lose `http.DefaultTransport` defaults (connection pooling, HTTP/2, timeouts). Use `http.DefaultTransport.(*http.Transport).Clone()` as your starting point and modify the clone. See [Custom Transport](#custom-transport) in the User Guide.

```go
cert, _ := tls.LoadX509KeyPair("client.crt", "client.key")
tp := http.DefaultTransport.(*http.Transport).Clone()
tp.TLSClientConfig.Certificates = []tls.Certificate{cert}

client, err := opensearchapi.NewClient(opensearchapi.Config{
    Client: opensearch.Config{
        Addresses: []string{"https://localhost:9200"},
        Transport: tp,
    },
})
```

The authentication mechanism used for health checks is identical to that used for all other requests; there is nothing special about the health endpoint.

### Creating a Minimal Health Check Role

The following creates a role with the least privilege necessary for health checking. This is appropriate for service accounts whose only job is monitoring cluster availability.

**Option A: Security REST API (dynamic, no restart required)**

This requires an authenticated admin user:

```
PUT /_plugins/_security/api/roles/health_check
{
  "cluster_permissions": [
    "cluster:monitor/health"
  ]
}
```

**Option B: `roles.yml` (file-based, requires `securityadmin` reload or restart)**

```yaml
health_check:
  cluster_permissions:
    - "cluster:monitor/health"
```

Then create the user and map the role:

```
PUT /_plugins/_security/api/internalusers/health_check_user
{
  "password": "changeme"
}

PUT /_plugins/_security/api/rolesmapping/health_check
{
  "users": ["health_check_user"]
}
```

### Pre-Existing Roles

Before creating a custom role, check whether an existing role already provides sufficient access:

| Role | Permissions | Notes |
| --- | --- | --- |
| `cluster_monitor` | `cluster:monitor/*` | Covers health, stats, and all monitoring endpoints. Broader than necessary for health checks alone. |
| `opensearch_dashboards_server` | Includes cluster monitoring among other privileges. | Intended for the Dashboards service account. |

The minimal custom `health_check` role with only `cluster:monitor/health` follows the principle of least privilege.

## Transitioning from an Unsecured Cluster

When a cluster transitions from no security (Security plugin disabled) to security enabled, the behavior changes:

| Before security | After security |
| --- | --- |
| All requests succeed with no credentials. | Requests without valid credentials return **401**. |
| No permission checks. | Requests with valid credentials but missing `cluster:monitor/health` privilege return **403**. |

If the client is performing health checks and the cluster enables security, health probes will begin returning 401. This is expected. The client should surface this condition clearly rather than reporting the cluster as unhealthy: the cluster is reachable, but credentials need to be configured.

**Recommended transition steps:**

1. Create the health check role and user (see above) before enabling security.
2. Configure client credentials to match.
3. Enable the Security plugin.
4. Verify health checks succeed with `200`.

## Cluster Blocks

The `/_cluster/health` endpoint is explicitly exempted from cluster-level blocks. Even when the cluster has a global read-only block or a full block active, this endpoint responds normally. This is intentional: administrators need to check health to determine _why_ the cluster is blocked.

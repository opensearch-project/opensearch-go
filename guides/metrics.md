# Client-Side Metrics

The opensearch-go transport exposes a pull-based metrics API that returns a point-in-time snapshot of request counters, connection pool state, per-connection health, policy-level breakdowns, and router cache state. All fields are JSON-tagged for easy serialization.

## Quick Start

```go
m, err := client.Metrics()
if err != nil {
    log.Fatal(err)
}

data, _ := json.MarshalIndent(m, "", "  ")
fmt.Println(string(data))
```

The `Metrics()` method is available on `opensearch.Client`. It returns an `opensearchtransport.Metrics` struct and an error (non-nil only if metrics are disabled or a callback fails).

---

## Metrics Struct

The top-level `Metrics` struct contains aggregate counters, per-connection details, per-policy pool snapshots, and router cache state.

### Request Counters

| Field       | Type          | Description                               |
| ----------- | ------------- | ----------------------------------------- |
| `requests`  | `int`         | Total requests performed by the transport |
| `failures`  | `int`         | Total request failures                    |
| `responses` | `map[int]int` | Response count by HTTP status code        |

### Connection Pool State

| Field                 | Type  | Description                                 |
| --------------------- | ----- | ------------------------------------------- |
| `live_connections`    | `int` | Non-dead connections (active + standby)     |
| `dead_connections`    | `int` | Connections in the dead list                |
| `standby_connections` | `int` | Connections in the standby partition        |
| `overloaded_servers`  | `int` | Connections the client considers overloaded |

### Connection Lifecycle Counters

| Field                  | Type  | Description                                          |
| ---------------------- | ----- | ---------------------------------------------------- |
| `connections_promoted` | `int` | Dead to ready transitions (successful resurrections) |
| `connections_demoted`  | `int` | Ready to dead transitions                            |
| `zombie_connections`   | `int` | Dead connections forcibly retried                    |
| `standby_promotions`   | `int` | Standby to active transitions                        |
| `standby_demotions`    | `int` | Active to standby transitions                        |

### Health Check Counters

| Field                   | Type  | Description                                        |
| ----------------------- | ----- | -------------------------------------------------- |
| `health_checks`         | `int` | Baseline `GET /` health checks performed           |
| `cluster_health_checks` | `int` | `GET /_cluster/health?local=true` checks performed |
| `health_checks_success` | `int` | Successful health check outcomes                   |
| `health_checks_failed`  | `int` | Failed health check outcomes                       |

---

## ConnectionMetric

Each connection produces a `ConnectionMetric` in `Metrics.Connections`. Connections are deduplicated: the same `*Connection` appearing in multiple policy pools is reported once.

### Core Fields

| Field              | JSON               | Type         | Description                         |
| ------------------ | ------------------ | ------------ | ----------------------------------- |
| `URL`              | `url`              | `string`     | Node URL                            |
| `Failures`         | `failures`         | `int`        | Failure count (omitted when zero)   |
| `IsDead`           | `dead`             | `bool`       | In the dead list                    |
| `IsStandby`        | `standby`          | `bool`       | In the standby partition            |
| `IsOverloaded`     | `overloaded`       | `bool`       | Marked overloaded by stats poller   |
| `IsWarmingUp`      | `warming_up`       | `bool`       | In warmup phase after promotion     |
| `IsHealthChecking` | `health_checking`  | `bool`       | Currently being health-checked      |
| `NeedsCatUpdate`   | `needs_cat_update` | `bool`       | Shard placement data is stale       |
| `Weight`           | `weight`           | `int`        | Effective weight for selection      |
| `DeadSince`        | `dead_since`       | `*time.Time` | When the connection was marked dead |
| `OverloadedSince`  | `overloaded_since` | `*time.Time` | When overload was detected          |
| `State`            | `state`            | `ConnState`  | Packed connection state word        |

### Router Fields

Populated when request routing is active and the connection has observed traffic.

| Field       | JSON         | Type       | Description                                                        |
| ----------- | ------------ | ---------- | ------------------------------------------------------------------ |
| `RTTBucket` | `rtt_bucket` | `*int64`   | Quantized RTT tier (lower is closer)                               |
| `RTTMedian` | `rtt_median` | `*string`  | Median RTT as a human-readable duration                            |
| `EstLoad`   | `est_load`   | `*float64` | Estimated load: `inFlight / cwnd`                                  |
| `MCSR`      | `mcsr`       | `*int`     | Adaptive `max_concurrent_shard_requests` value (nil when disabled) |

### Node Metadata

| Field        | JSON         | Type       | Description                                            |
| ------------ | ------------ | ---------- | ------------------------------------------------------ |
| `Meta.ID`    | `meta.id`    | `string`   | OpenSearch node ID                                     |
| `Meta.Name`  | `meta.name`  | `string`   | OpenSearch node name                                   |
| `Meta.Roles` | `meta.roles` | `[]string` | Node roles (`data`, `ingest`, `cluster_manager`, etc.) |

---

## PolicySnapshot

Each policy with a connection pool produces a `PolicySnapshot` in `Metrics.Policies`. Policies register their snapshot callback at construction time; the `Metrics()` method invokes all callbacks on each poll.

| Field                 | JSON                    | Type     | Description                                                            |
| --------------------- | ----------------------- | -------- | ---------------------------------------------------------------------- |
| `Name`                | `name`                  | `string` | Policy name (e.g., `role:data`, `roundrobin`, `coordinator`, `client`) |
| `Enabled`             | `enabled`               | `bool`   | Whether this policy is currently routing traffic                       |
| `ActiveCount`         | `active_count`          | `int`    | Connections in the active partition                                    |
| `StandbyCount`        | `standby_count`         | `int`    | Connections in the standby partition                                   |
| `DeadCount`           | `dead_count`            | `int`    | Connections in the dead list                                           |
| `ActiveListCap`       | `active_list_cap`       | `int`    | Capacity of the active partition                                       |
| `WarmingCount`        | `warming_count`         | `int`    | Connections in warmup                                                  |
| `HealthCheckingCount` | `health_checking_count` | `int`    | Connections being health-checked                                       |
| `Requests`            | `requests`              | `int64`  | Connections returned by `Next()`                                       |
| `Successes`           | `successes`             | `int64`  | Resurrections via `OnSuccess()`                                        |
| `Failures`            | `failures`              | `int64`  | Demotions via `OnFailure()`                                            |
| `WarmupSkips`         | `warmup_skips`          | `int64`  | Requests skipped during warmup                                         |
| `WarmupAccepts`       | `warmup_accepts`        | `int64`  | Requests accepted during warmup                                        |

The `client` pool snapshot is always present and represents the flat connection pool shared by all policies.

---

## RouterSnapshot

When request routing is active, `Metrics.Router` contains a `RouterSnapshot` with per-index routing state and the effective cache configuration.

### RouterSnapshotConfig

| Field             | JSON                  | Type      | Description                                  |
| ----------------- | --------------------- | --------- | -------------------------------------------- |
| `MinFanOut`       | `min_fan_out`         | `int`     | Minimum candidates per routing decision      |
| `MaxFanOut`       | `max_fan_out`         | `int`     | Maximum candidates per routing decision      |
| `DecayFactor`     | `decay_factor`        | `float64` | Exponential decay for request rate smoothing |
| `FanOutPerReq`    | `fan_out_per_request` | `float64` | Fan-out growth per request                   |
| `IdleEvictionTTL` | `idle_eviction_ttl`   | `string`  | Time before idle index slots are evicted     |

### IndexRouterState

Each index with an active routing slot produces an entry in `Router.Indexes`.

| Field         | JSON           | Type         | Description                                      |
| ------------- | -------------- | ------------ | ------------------------------------------------ |
| `Name`        | `name`         | `string`     | Index name                                       |
| `FanOut`      | `fan_out`      | `int`        | Current fan-out (candidate count) for this index |
| `ShardNodes`  | `shard_nodes`  | `int`        | Nodes with known shard placement for this index  |
| `RequestRate` | `request_rate` | `float64`    | Smoothed request rate                            |
| `IdleSince`   | `idle_since`   | `*time.Time` | When the index became idle (nil if active)       |

---

## Polling Example

Poll metrics on a timer for logging or export to an external monitoring system.

```go
ticker := time.NewTicker(30 * time.Second)
defer ticker.Stop()

for range ticker.C {
    m, err := client.Metrics()
    if err != nil {
        log.Printf("metrics error: %v", err)
        continue
    }

    log.Printf("requests=%d failures=%d live=%d dead=%d standby=%d overloaded=%d",
        m.Requests, m.Failures,
        m.LiveConnections, m.DeadConnections,
        m.StandbyConnections, m.OverloadedServers)

    // Per-connection detail
    for _, c := range m.Connections {
        cm := c.(opensearchtransport.ConnectionMetric)
        if cm.RTTMedian != nil {
            log.Printf("  %s rtt=%s load=%.2f",
                cm.URL, *cm.RTTMedian, safeFloat(cm.EstLoad))
        }
    }

    // Per-policy breakdown
    for _, p := range m.Policies {
        log.Printf("  policy %s: active=%d standby=%d dead=%d req=%d fail=%d",
            p.Name, p.ActiveCount, p.StandbyCount, p.DeadCount, p.Requests, p.Failures)
    }

    // Router cache (when routing is active)
    if m.Router != nil {
        for _, idx := range m.Router.Indexes {
            log.Printf("  index %s: fan_out=%d shard_nodes=%d rate=%.1f",
                idx.Name, idx.FanOut, idx.ShardNodes, idx.RequestRate)
        }
    }
}

func safeFloat(f *float64) float64 {
    if f == nil {
        return 0
    }
    return *f
}
```

## JSON Export

The `Metrics` struct is fully JSON-tagged. Serialize with `encoding/json` for structured logging or HTTP endpoints:

```go
m, _ := client.Metrics()
data, _ := json.Marshal(m)
w.Header().Set("Content-Type", "application/json")
w.Write(data)
```

## Prometheus / OpenTelemetry

The metrics API is pull-based: call `client.Metrics()` inside your collector's `Collect()` or OTEL callback. Map fields to gauges and counters as appropriate:

| Metrics field                | Metric type        | Suggested name                                  |
| ---------------------------- | ------------------ | ----------------------------------------------- |
| `Requests`                   | Counter            | `opensearch_client_requests_total`              |
| `Failures`                   | Counter            | `opensearch_client_failures_total`              |
| `Responses[code]`            | Counter (labeled)  | `opensearch_client_responses_total{code="200"}` |
| `LiveConnections`            | Gauge              | `opensearch_client_connections_live`            |
| `DeadConnections`            | Gauge              | `opensearch_client_connections_dead`            |
| `StandbyConnections`         | Gauge              | `opensearch_client_connections_standby`         |
| `OverloadedServers`          | Gauge              | `opensearch_client_connections_overloaded`      |
| `ConnectionMetric.EstLoad`   | Gauge (per-node)   | `opensearch_client_node_est_load{node="..."}`   |
| `ConnectionMetric.MCSR`      | Gauge (per-node)   | `opensearch_client_node_mcsr{node="..."}`       |
| `PolicySnapshot.ActiveCount` | Gauge (per-policy) | `opensearch_client_policy_active{policy="..."}` |
| `IndexRouterState.FanOut`    | Gauge (per-index)  | `opensearch_client_index_fan_out{index="..."}`  |

---

## Observer API

For event-driven observability (as opposed to polling), implement the `ConnectionObserver` interface and pass it via `opensearchtransport.WithObserver()`. The observer receives callbacks for connection lifecycle events (promote, demote, overload), routing decisions, health checks, and shard map invalidations. See the [routing guide](routing.md) for details on observer events.

The metrics API and observer API are complementary: metrics give you aggregate snapshots for dashboards, while the observer gives you per-event detail for tracing and debugging.

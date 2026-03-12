# Request Routing and Connection Management

The opensearch-go v4 transport layer replaces round-robin connection selection with a per-request scoring model that routes operations to shard-hosting nodes based on network proximity, server-side load, and data placement. The result is elimination of coordinator proxy hops, concentration of OS page-cache utilization, AZ-aware load distribution, and automatic resilience against node failures and overload.

This document covers the scoring formula and its three input signals, the policy chain architecture, the connection pool lifecycle, the efficiency model for cross-AZ hop reduction, and the operational knobs available to operators.

## Quick Start

```go
import "github.com/opensearch-project/opensearch-go/v4/opensearchtransport"

router := opensearchtransport.NewDefaultRouter()

client, err := opensearch.NewClient(opensearch.Config{
    Addresses:             []string{"https://node1:9200", "https://node2:9200"},
    DiscoverNodesOnStart:  true,
    DiscoverNodesInterval: 5 * time.Minute,
    Router:                router,
})
```

With no additional configuration, this enables:

- Consistent per-index node assignment via rendezvous hashing
- RTT-based availability-zone preference (local nodes preferred, with overflow to remote)
- Operation-aware shard cost (replica-hosting nodes preferred for reads, primary-hosting for writes)
- Per-pool AIMD congestion control: capacity-aware connection scoring via `(inFlight + 1) / cwnd`
- Shard-aware node partitioning derived from `/_cat/shards` data
- Murmur3 shard-exact routing for `?routing=` requests and document ID operations

Without a `Router`, the client uses round-robin across all discovered nodes (the pre-existing default behavior).

---

## Table of Contents

1. [Motivation](#1-motivation)
2. [Architecture Overview](#2-architecture-overview)
3. [Request Routing Pipeline](#3-request-routing-pipeline)
4. [Connection Scoring](#4-connection-scoring)
5. [Per-Index Affinity and Fan-Out](#5-per-index-affinity-and-fan-out)
6. [Shard-Exact Routing](#6-shard-exact-routing)
7. [Thread Pool Congestion Control](#7-thread-pool-congestion-control)
8. [Connection Pool Lifecycle](#8-connection-pool-lifecycle)
9. [Health Checking and Load Shedding](#9-health-checking-and-load-shedding)
10. [Resurrection and Backoff](#10-resurrection-and-backoff)
11. [Error Handling and Partial Failures](#11-error-handling-and-partial-failures)
12. [Cost and Efficiency Model](#12-cost-and-efficiency-model)
13. [Operational Guide](#13-operational-guide)
14. [Configuration Reference](#14-configuration-reference)
15. [Appendix A: Summary Formulas](#appendix-a-summary-formulas)
16. [Appendix B: Benchmark Data](#appendix-b-benchmark-data)

---

## 1. Motivation

### The Coordinator Proxy Tax

In a standard OpenSearch cluster, any node can serve as the coordinating node for any request. The default Go client (and most official clients) selects nodes via round-robin -- uniformly at random from the discovered set. When a request lands on a node that does not host the target index's shards, that node becomes a **coordinator proxy**: it forwards the request internally, receives the response on the internal network, buffers it in JVM heap, and relays it back to the client.

```
Round-Robin (before)                         Request Routed (after)

Client ──── Node B ────────── Node A         Client ──── Node A
            coordinator       has shard                  has shard, direct
            proxy hop ++
            JVM heap ++
            failure domain ++
```

The probability of hitting the proxy path under round-robin is:

```
P(proxy) = 1 - n_s / N
```

where `n_s` is the number of nodes hosting shards for the target index and `N` is total cluster nodes. For the median index on a production cluster, this fraction is startlingly high:

| Cluster Size | Shard Nodes (n_s) | P(proxy) |
| ------------ | ----------------- | -------- |
| 6-node       | 2                 | 67%      |
| 32-node      | 5                 | 84%      |
| 200-node     | 4                 | 98%      |
| 256-node     | 2                 | 99%      |

For a typical small-to-medium index on a 200-node cluster, 98 out of 100 requests are needlessly proxied. Only the two or three largest indexes (with shards spread across many nodes) have a meaningful chance of direct hits under round-robin.

### What the Proxy Hop Costs

Each unnecessary proxy hop imposes five costs:

1. **Latency.** An additional intra-cluster network round-trip (250-500 us same-AZ, 1-2 ms cross-AZ).
2. **Bandwidth.** The full response payload traverses two extra links (coordinator-to-shard and shard-to-coordinator).
3. **JVM heap.** The coordinator buffers the entire response before forwarding, contributing to GC pressure and circuit-breaker utilization.
4. **Availability.** Each serial network link is a failure domain. A proxied request traverses 3 links (client-coordinator, coordinator-shard, shard-coordinator) versus 2 for a direct request.
5. **Page cache pollution.** Coordinator proxying pulls response payloads through nodes that don't host the index, fragmenting the OS page cache across all indexes rather than concentrating it on local shard data.

### The Goal

Route requests directly to nodes that host the relevant shard data, while adapting to network proximity, server-side load, and cluster topology changes. Do this with zero heap allocations in the request path, sub-microsecond overhead, and no server-side changes.

---

## 2. Architecture Overview

The transport layer sits between the API surface (`opensearchapi`) and the HTTP connection pool. Every request passes through the same path:

```
client.Search(ctx, ...)
  │
  v
transport.Perform(req)
  │
  ├── router.Route(ctx, req)               ── policy chain evaluation
  │     │
  │     ├── IfEnabledPolicy                ── coordinating-only node short-circuit
  │     ├── MuxPolicy                      ── trie lookup by HTTP method + path
  │     │     └── poolRouter.Eval()        ── connection scoring over role pool
  │     └── RoundRobinPolicy               ── fallback
  │
  ├── conn.addInFlight(poolName)           ── per-pool in-flight tracking
  ├── http.Transport.RoundTrip(req)        ── network I/O
  ├── conn.releaseInFlight(poolName)       ── decrement in-flight
  └── router.OnSuccess / OnFailure         ── connection health bookkeeping
```

### Component Map

| Component            | Responsibility                                                                             |
| -------------------- | ------------------------------------------------------------------------------------------ |
| **Router**           | Top-level coordinator. Tries policies in sequence, returns the first `NextHop`.            |
| **Policy Chain**     | Chain-of-responsibility pattern. Policies return a connection or pass through.             |
| **MuxPolicy**        | Zero-allocation trie matching ~124 URL patterns to 11 route categories.                    |
| **poolRouter**       | Wraps a role policy with per-index connection scoring (rendezvous hash + scoring formula). |
| **RolePolicy**       | Maintains a connection pool filtered by node roles (data, ingest, search, etc.).           |
| **Connection Pool**  | Three-partition structure: active, standby, dead. Weighted round-robin selection.          |
| **Index Slot Cache** | Per-index routing state: shard placement, fan-out, scoring history.                        |
| **RTT Ring Buffer**  | Per-connection rolling median of health-check RTTs, quantized to power-of-2 buckets.       |
| **AIMD Controller**  | Per-connection, per-thread-pool congestion window driven by server stats.                  |

### Three Pre-Built Routers

| Router          | Constructor             | Use Case                                                |
| --------------- | ----------------------- | ------------------------------------------------------- |
| **Default**     | `NewDefaultRouter()`    | Role-based + connection scoring.                        |
| **Mux**         | `NewMuxRouter()`        | Role-based without scoring. Useful for comparison.      |
| **Round-Robin** | `NewRoundRobinRouter()` | Coordinating-only preference with round-robin fallback. |

All three share the same three-level policy chain:

1. **Coordinating-only nodes** -- if any exist, routes all traffic to them exclusively.
2. **Operation-specific routing** -- bulk→ingest, search→search/data, refresh→data, etc.
3. **Round-robin fallback** -- high-availability safety net.

---

## 3. Request Routing Pipeline

### Route Classification

The MuxPolicy trie maps URL patterns to 11 route categories. Each category specifies a role chain (with fallback), a thread pool name (for congestion tracking), and a shard cost table (read vs write).

| Category       | Example Endpoints                            | Role Chain           | Pool          | Cost Table |
| -------------- | -------------------------------------------- | -------------------- | ------------- | ---------- |
| searchRead     | `_search`, `_msearch`, `_count`, `_validate` | search → data → null | `search`      | reads      |
| getRead        | `_doc/{id}`, `_source/{id}`, `_mget`         | search → data → null | `get`         | reads      |
| dataWrite      | `_doc/{id}` (PUT/POST), `_create`, `_update` | data → null          | `write`       | writes     |
| ingestWrite    | `_bulk`, `_reindex`                          | ingest → null        | `write`       | writes     |
| dataRefresh    | `_refresh`                                   | data → null          | `refresh`     | writes     |
| dataFlush      | `_flush`                                     | data → null          | `flush`       | writes     |
| dataForceMerge | `_forcemerge`                                | data → null          | `force_merge` | writes     |
| dataMgmt       | `_recovery`, `_shard_stores`, `_stats`       | data → null          | `management`  | reads      |
| searchMgmt     | `_field_caps`                                | search → data → null | `management`  | reads      |
| ingestMgmt     | `_ingest/pipeline`                           | ingest → null        | `management`  | reads      |
| warmMgmt       | `_snapshot/{repo}/_mount`                    | warm → data → null   | `management`  | reads      |

Role chains use `IfEnabledPolicy` for fallback: if dedicated search nodes exist, use them; otherwise fall to data nodes; if neither matches, pass through to round-robin.

### Policy Chain Evaluation Overhead

| Step | Policy             | Cost (ns) | Allocations |
| ---- | ------------------ | --------- | ----------- |
| 1    | `IfEnabledPolicy`  | ~151      | 0           |
| 2    | `MuxPolicy`        | ~181      | 0           |
| 3    | `RoundRobinPolicy` | ~154      | 0           |

The routing decision is 0.05-0.18% of a same-AZ network RTT and 0.01-0.05% of a cross-AZ RTT:

| Operation      | Router Overhead | Same-AZ RTT (250-500 us) | Overhead   |
| -------------- | --------------- | ------------------------ | ---------- |
| Search         | ~259 ns         | 250,000-500,000 ns       | 0.05-0.10% |
| Get (document) | ~452 ns         | 250,000-500,000 ns       | 0.09-0.18% |
| Bulk write     | ~231 ns         | 250,000-500,000 ns       | 0.05-0.09% |
| Cluster health | ~187 ns         | 250,000-500,000 ns       | 0.04-0.07% |

For every nanosecond spent on routing, the client saves 500-4,000 ns of server-side processing and network transit by avoiding the coordinator hop.

### Three Routing Paths: Document, Index, and Cluster

Once the MuxPolicy classifies a request and the poolRouter receives the role-filtered pool, the routing path depends on what the request URL contains. There are three distinct paths, each with a different level of specificity:

#### Document Path (`DocRouter`)

Activated when the URL contains both an index and a document ID (e.g., `GET /orders/_doc/abc123`). This is the most precise path -- it narrows the candidate set to the 1-3 nodes hosting the exact shard for that document.

```
    GET /orders/_doc/abc123
                │
                ▼
    ┌──────────────────────────────────────────────────────┐
    │  1. EXTRACT                                          │
    │     extractDocumentFromPath("/orders/_doc/abc123")   │
    │     → index="orders", docID="abc123"                 │
    │     routingKey = ?routing= param or docID            │
    └──────────────┬───────────────────────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────┐
    │  2. SHARD-EXACT LOOKUP                               │
    │     shard = floorMod(murmur3(routingKey),            │
    │               routingNumShards) / routingFactor      │
    │     nodes = shardMap[shard]                          │
    │     → [primary: node2, replica: node5]               │
    └──────────────┬───────────────────────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────┐
    │  3. SCORE (1-3 candidates)                           │
    │     For each node hosting this exact shard:          │
    │       score = rttBucket × (inFlight+1)/cwnd          │
    │             × shardCost(primary vs replica)          │
    │     Return lowest-scoring node                       │
    └──────────────────────────────────────────────────────┘
```

The murmur3 hash implementation matches the server's `OperationRouting.generateShardId()` exactly (UTF-16 LE encoding, same constants). The candidate set is the smallest possible -- only nodes hosting the specific shard for this document. If the shard map is not yet available (first seconds after startup), the path falls back to the index path with rendezvous hashing.

#### Index Path (`IndexRouter`)

Activated when the URL contains an index but no document ID (e.g., `POST /orders/_search`). This is the standard path for searches, aggregations, and bulk operations. It uses per-index affinity via rendezvous hashing and fan-out.

```
    POST /orders/_search
                │
                ▼
    ┌──────────────────────────────────────────────────────┐
    │  1. SLOT LOOKUP                                      │
    │     slot = indexSlotCache.getOrCreate("orders")      │
    │     slot.requestDecay += 1.0                         │
    │     K = effectiveFanOut(slot)                        │
    │     shardNodes = slot.shardNodeNames                 │
    └──────────────┬───────────────────────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────┐
    │  2. CANDIDATE SELECTION (K nodes)                    │
    │     Partition: shard-hosting nodes first             │
    │     Fill tier-by-tier (nearest RTT bucket first)     │
    │     Within tier: rendezvous hash for stability       │
    │     Rotate by jitter offset                          │
    │     → [node1, node3, node5, node7, node2] (K=5)      │
    └──────────────┬───────────────────────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────┐
    │  3. SCORE (K candidates)                             │
    │     For each candidate in fan-out set:               │
    │       shardInfo = slot.shardNodeInfo(candidate)      │
    │       score = rttBucket × (inFlight+1)/cwnd          │
    │             × shardCost(shardInfo, operation)        │
    │     Return lowest-scoring node                       │
    └──────────────────────────────────────────────────────┘
```

The index slot provides per-index state: shard placement, fan-out sizing, and request volume tracking. The rendezvous hash ensures that the same index consistently maps to the same candidate set (maximizing page-cache hits), while the scoring formula dynamically balances load within that set. Fan-out grows with request volume and contracts when traffic subsides.

If the request also contains a `?routing=` parameter, the index path first attempts shard-exact routing (same murmur3 path as the document router). It falls back to rendezvous hashing only if the shard map lookup fails.

#### Cluster Path

Activated when the URL has no index component (e.g., `GET /_cluster/health`, `GET /_cat/nodes`). This is the simplest path -- no shard awareness, no rendezvous hashing, no fan-out. The request goes to whichever node is nearest and has the most capacity.

```
    GET /_cluster/health
                │
                ▼
    ┌──────────────────────────────────────────────────────┐
    │  1. EXTRACT                                          │
    │     extractIndexFromPath("/_cluster/health")         │
    │     → "" (no index)                                  │
    └──────────────┬───────────────────────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────────────────────┐
    │  2. SCORE ALL ACTIVE CONNECTIONS                     │
    │     For each active connection:                      │
    │       score = rttBucket × (inFlight+1)/cwnd          │
    │             × costUnknown (32.0 -- constant,         │
    │               cancels out across all nodes)          │
    │     Return lowest-scoring node                       │
    └──────────────────────────────────────────────────────┘
```

Since `costUnknown` is applied uniformly to all nodes, it cancels out in comparison. Selection is driven purely by RTT (prefer same-AZ) and congestion (prefer idle nodes). There is no index slot, no fan-out, and no shard placement data. The candidate set is all active connections in the role pool.

#### Path Comparison

| Aspect             | Document Path           | Index Path             | Cluster Path           |
| ------------------ | ----------------------- | ---------------------- | ---------------------- |
| Trigger            | Index + document ID     | Index, no document ID  | No index in URL        |
| Candidates         | 1-3 (exact shard hosts) | K (rendezvous fan-out) | All active connections |
| Shard awareness    | Exact shard via murmur3 | Per-node shard counts  | None                   |
| Fan-out            | None (exact set)        | Dynamic, per-index     | None (score all)       |
| Rendezvous hashing | Fallback only           | Primary mechanism      | None                   |
| Shard cost         | Primary vs replica      | Primary vs replica     | costUnknown (uniform)  |
| Index slot         | Shared with index path  | Created per-index      | None                   |
| Page cache benefit | Maximum (exact shard)   | High (shard-host bias) | None                   |

### Shared Connections Across Policies

Every policy in the chain (RoundRobinPolicy, RolePolicy, MuxPolicy) owns its own `multiServerPool` with independent ready/dead lists and active/standby partitions. However, all pools that contain a given node share the **same `*Connection` pointer**:

```
  ┌─────────────────────┐   ┌──────────────────────┐
  │ RoundRobinPolicy    │   │ RolePolicy("data")   │
  │                     │   │                      │
  │ pool.ready:         │   │ pool.ready:          │
  │   [*A, *B, *C, *D]  │   │   [*A, *C]           │
  │ pool.dead:          │   │ pool.dead:           │
  │   []                │   │   []                 │
  └────────┬────────────┘   └────────┬─────────────┘
           │                         │
           │      ┌──────────────────┤
           │      │                  │
           ▼      ▼                  ▼
  ┌────────────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐
  │ Connection A   │  │ Conn B  │  │ Conn C  │  │ Conn D  │
  │ state: atomic  │  │ state   │  │ state   │  │ state   │
  │ URL: :9200     │  │ :9201   │  │ :9202   │  │ :9203   │
  │ roles: [data]  │  │ [ingest]│  │ [data]  │  │ [ingest]│
  └────────────────┘  └─────────┘  └─────────┘  └─────────┘
```

This design has a critical consequence: **any request flowing through any policy updates connection state in real time**. When a request through the data RolePolicy increments `inFlight` on Connection A, that count is immediately visible to the RoundRobinPolicy and every other policy that holds a pointer to Connection A. There is no synchronization step, no copy, and no lag -- the atomic counters on the `Connection` are the single source of truth.

**Pool position updates are lazy.** The `Connection.state` atomic encodes the _target_ position (active, standby, dead) via lifecycle bits, but each pool only moves the connection within its own `ready[]`/`dead[]` slices when that pool is next evaluated. This means:

- When a connection fails via `OnFailure()`, the lifecycle bits change atomically and instantly. The policy that detected the failure moves the connection to its own dead list immediately.
- Other policies discover the failure on their _next_ `Next()` or `DiscoveryUpdate()` call. They read the connection's lifecycle bits, see that the position bits (`lcActive|lcStandby`) are cleared, and evict the connection to their own dead list at that point.
- The connection itself is authoritative -- its atomic state word is the single source of truth. Each policy's pool position is a local cache that is reconciled lazily.

This amortizes pool management cost: expensive operations (acquiring write locks, shuffling slices) happen only when a policy is actively evaluated, not eagerly across all policies the moment something changes. For policies that serve infrequent request types (e.g., `force_merge`), the pool is untouched until the next request arrives, at which point it catches up in a single pass.

**Concurrency model.** The request path reads all scoring inputs -- RTT bucket, in-flight count, congestion window, shard cost, lifecycle bits -- via atomic loads only. No locks are acquired during `Eval()`. Writes that imply a multi-part state change (pool position transitions, discovery updates, warmup initialization, AIMD window adjustments) are guarded by the appropriate connection pool or connection mutex to avoid concurrent mutations and inconsistent connection state. The expensive work -- topology discovery, stats polling, shard catalog refresh, resurrection health checks -- runs in background goroutines that update atomics behind those mutexes. The request hot path pays only the cost of reading the atomics; all mutation cost is deferred to asynchronous maintenance loops.

---

## 4. Connection Scoring

### The Formula

Once the candidate set is determined, each candidate is scored:

```
score = rttBucket × (inFlight + 1) / cwnd × shardCost
```

**Lower score wins.** The three multiplicative factors are independent signals:

| Factor                  | Source                          | What It Measures                                                            |
| ----------------------- | ------------------------------- | --------------------------------------------------------------------------- |
| `rttBucket`             | `conn.rttRing.medianBucket()`   | Network proximity. Power-of-2 quantized RTT. Same-AZ: 8-9, cross-AZ: 10-11. |
| `(inFlight + 1) / cwnd` | Per-pool AIMD congestion window | Utilization ratio. Atomic per-pool counter.                                 |
| `shardCost`             | Shard cost table lookup         | Whether the node hosts relevant shards.                                     |

When a thread pool is overloaded (rejected requests or HTTP 429), the score is `math.MaxFloat64` -- the node is effectively removed from consideration for that pool.

### RTT Bucketing

Health-check RTTs are quantized to power-of-2 buckets (floor 8 = 256 us). This provides a stable network proximity signal immune to jitter:

```
Same-AZ nodes:     bucket 8 (256 us)  to  bucket 9  (512 us)
Cross-AZ nodes:    bucket 10 (1 ms)   to  bucket 11 (2 ms)
Cross-region:      bucket 12 (4 ms)+
```

The median of the last N samples (ring buffer sized to the health check interval) is used rather than the latest sample, preventing a single slow health check from destabilizing routing.

Power-of-two bucketing maps naturally onto network latency tiers. Each network hop roughly doubles round-trip time, and each doubling increments the bucket by exactly 1:

```
    Network tier         Typical RTT    Bucket    Ratio vs same-AZ
    ──────────────────   ───────────    ──────    ──────────────────
    Same rack            ~100 us        8         1x       <- floor clamps to 8
    Same AZ              ~200 us        8         1x       <- floor clamps to 8
    Cross-AZ (near)      ~1 ms          9         1.125x
    Cross-AZ (far)       ~3 ms          11        1.375x
    Cross-region (near)  ~20 ms         14        1.75x
    Cross-region (far)   ~80 ms         16        2x


    Bucket value vs round-trip time (log2 scale)

    bucket
     16 │                                                  ╭─ cross-region
        │                                              ╭───╯
     14 │                                         ╭────╯
        │                                     ╭───╯
     12 │                                 ╭───╯
        │                             ╭───╯
     11 │                         ╭───╯
      9 │────────────────── ╭─────╯
      8 │━━━━━━━━━━━━━━━━━━━╯                          <- floor (sub-256us nodes are peers)
        └───────────┬──────┬─────────┬───────────┬──── RTT
                  200us    1ms      10ms       100ms

    The floor at bucket=8 is essential: nodes in the same AZ (<=256us)
    all land in bucket 8, so they compete on utilization and shard cost
    alone. Without the floor, a 100us node would get bucket 6, creating
    spurious differentiation between same-AZ peers.
```

### Shard Cost Tables

Different operations use different cost tables. Lower cost = preferred node.

**Read operations** (search, get, scroll, validate, rank_eval):

| Shard State  | Cost | Reason                                                      |
| ------------ | ---- | ----------------------------------------------------------- |
| Replica      | 1.0  | Preferred -- lock-free Lucene snapshot, no write contention |
| Primary      | 2.0  | Acceptable -- contends with concurrent writes               |
| Relocating   | 8.0  | Shard moving, may require proxy hop                         |
| Initializing | 16.0 | Shard not yet ready to serve                                |
| Unknown      | 32.0 | No shard data from discovery yet                            |

**Write operations** (index, bulk, update, delete):

| Shard State  | Cost | Reason                                       |
| ------------ | ---- | -------------------------------------------- |
| Primary      | 1.0  | Preferred -- write lands directly on primary |
| Replica      | 2.0  | Must proxy through primary -- adds a hop     |
| Relocating   | 8.0  | Shard moving, may require proxy hop          |
| Initializing | 16.0 | Shard not yet ready to serve                 |
| Unknown      | 32.0 | No shard data from discovery yet             |

The 32x penalty for unknown/non-shard nodes is the key to affinity. A non-shard node at idle still scores 16x worse than a busy shard node:

```
Shard node, busy:      11 × (13+1)/13 × 2.0  =  23.7
Non-shard node, idle:  11 ×  (0+1)/13 × 32.0 =  27.1  ← still loses
```

### Score Components Visualization

The three multiplicative components interact to create a composite score that ranks every candidate connection:

```
    score = rttBucket * (inFlight + 1) / cwnd * shardCostMultiplier
            ────┬───   ──────────┬──────────   ──────────┬─────────
                │                │                       │
                │                │                       +-- Shard cost (operation-aware)
                │                │                           reads: replica=1.0  primary=2.0
                │                │                           writes: primary=1.0  replica=2.0
                │                │                           relocating=8.0  initializing=16.0
                │                │                           unknown=32.0 (both)
                │                │
                │                +-- Utilization ratio (per-pool AIMD)
                │                    idle=1/cwnd  busy=cwnd/cwnd=1.0
                │                    (inFlight from atomic counter; cwnd from stats poller)
                │
                +-- Network proximity (stable, power-of-two buckets)
                    same-AZ=8  cross-AZ=9-11  cross-region=14-16


    Example: 6-node cluster, index "orders" with fan-out K=4 (read path)
    (search pool: cwnd=13 on all nodes; RTT measured from client AZ us-e1)

    Node       AZ     RTT Bucket  InFlight  Cwnd  Util   Shard Cost      Score   Fan-out?  Rank
    ---------  -----  ----------  --------  ----  -----  --------------  ------  --------  ----
    node-1     us-e1  8           5         13    6/13   1.0 (replica)   3.69    K=4       3
    node-2     us-e1  8           2         13    3/13   2.0 (primary)   3.69    K=4       4
    node-3     us-e2  11          0         13    1/13   1.0 (replica)   0.85    K=4       1 <- winner
    node-4     us-e2  11          0         13    1/13   2.0 (primary)   1.69    K=4       2
    node-5     us-e3  14          0         13    1/13   1.0 (replica)   --      --        --
    node-6     us-e3  14          0         13    1/13   32.0 (unknown)  --      --        --

    Fan-out K=4 selected [node-1, node-2, node-3, node-4] via slot filling.
    node-5 and node-6 are not in the fan-out set and are not scored.
    node-3 wins this request (lowest score: idle remote replica node).
    After winning, node-3.inFlight becomes 1 and subsequent requests may
    pick node-4 or rotate back to local nodes as local requests complete.
```

### Concrete Scoring Example

Three-node cluster, search request for `my-index`:

```
Node A: bucket=8,  inFlight=2, cwnd=13, replica  (read cost 1.0)
  score = 8 × (2+1)/13 × 1.0 = 1.85

Node B: bucket=8,  inFlight=0, cwnd=13, primary  (read cost 2.0)
  score = 8 × (0+1)/13 × 2.0 = 1.23

Node C: bucket=11, inFlight=0, cwnd=13, replica   (read cost 1.0)
  score = 11 × (0+1)/13 × 1.0 = 0.85
```

Winner: **Node C** (score 0.85). Despite being cross-AZ (higher RTT bucket), its zero in-flight and preferred shard role make it the best choice. As Node C accumulates in-flight requests, its score rises and traffic shifts to A and B.

### AZ-Aware Crossover

The multiplicative score formula creates natural tier-based overflow. Crossover between a local node (bucket=8) and a remote node (bucket=B) is immediate when the local node has higher utilization:

```
    Score at various in-flight counts -- local (bucket=8) vs remote (bucket=11)
    (search pool cwnd=13 on both nodes; ignoring shard cost)

    score
    4.0 │                       ╭── local: 8 * (inFlight+1)/13
        │                  ╭────╯
    3.0 │             ╭────╯
        │        ╭────╯
    2.0 │   ╭────╯
        │╭──╯
    1.0 │╯
    0.85│━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  remote: 11 * 1/13
    0.62│── ── ── ── ── ── ── ── ── ──   local idle: 8 * 1/13
      0 └──────────────────────────────── inFlight (local)
        0      1      2      3      4

    At 0 in-flight, local wins (0.62 < 0.85). At 1 in-flight, local
    score is 1.23 > 0.85 and remote wins. Traffic interleaves from
    the first concurrent request.
```

At sustained load across multiple RTT tiers, the congestion-window scoring produces proportional traffic distribution:

```
    Steady-state traffic distribution: 6-node fan-out set across 3 AZs
    (cwnd=13 for search pool on all nodes)

    reqs/s
     40 │ ██ ██
        │ ██ ██
     30 │ ██ ██              ██ ██
        │ ██ ██              ██ ██         ██ ██       Per-tier:
     20 │ ██ ██              ██ ██         ██ ██         AZ-1: slightly more (lowest RTT)
        │ ██ ██              ██ ██         ██ ██         AZ-2: moderate
     10 │ ██ ██              ██ ██         ██ ██         AZ-3: slightly less (highest RTT)
        │ ██ ██              ██ ██         ██ ██
      0 └──────────────────────────────────────────
        n1     n2           n3     n4     n5     n6
        ──── AZ-1 ────     ──── AZ-2 ──  ── AZ-3 ──
        (bucket=8)         (bucket=11)    (bucket=14)

    Within each AZ, nodes split evenly (same bucket, so only utilization
    and shard cost differentiate). Primary-hosting nodes receive slightly
    less traffic on read routes due to the shard cost multiplier.
```

---

## 5. Per-Index Affinity and Fan-Out

### Index Slot Cache

Each index gets its own _slot_ in the affinity cache. Slots are independent -- one index's routing never interferes with another's.

```
indexSlotCache
┌───────────────────────────────────────────────────────────────┐
│                                                               │
│  "orders"         (2P/1R → 4 of 6 nodes have shards)          │
│  ├── shardNodes: [node1=primary, node2=replica, ...]          │
│  ├── fanOut: 4                                                │
│  └── routing: distributed by score across shard hosts         │
│                                                               │
│  "sessions"       (1P/1R → 2 of 6 nodes have shards)          │
│  ├── shardNodes: [node3=primary, node5=replica]               │
│  ├── fanOut: 2                                                │
│  └── routing: concentrated on 2 nodes (others excluded)       │
│                                                               │
│  "config"         (1P/0R → 1 node has the shard)              │
│  ├── shardNodes: [node4=primary]                              │
│  ├── fanOut: 2                                                │
│  └── routing: 100% to node4                                   │
│                                                               │
└───────────────────────────────────────────────────────────────┘
```

Each slot tracks shard placement, fan-out state, and request volume:

```
    indexSlot for "orders"
    ┌────────────────────────────────────────────────────────────────────┐
    │                                                                    │
    │  shardNodeNames ─────────────────────────────────── Who has data?  │
    │  ┌──────────────────────────────────────────────┐                  │
    │  │ "node-1" -> { Primaries: 0, Replicas: 2 }    │  from            │
    │  │ "node-3" -> { Primaries: 2, Replicas: 0 }    │  /_cat/shards    │
    │  │ "node-5" -> { Primaries: 1, Replicas: 1 }    │                  │
    │  └──────────────────────────────────────────────┘                  │
    │                                                                    │
    │  shardNodes: 3 ────────────────────────────── Fan-out floor input  │
    │                                                                    │
    │  requestDecay: 842.7 ─────────────── Request volume (decay=0.999)  │
    │  fanOut: 5 ──────────────────── Current effective fan-out (K=5)    │
    │                                                                    │
    │  idleSince: 0 ────────── Eviction clock (0 = active, nonzero =     │
    │                           nanos when counter dropped below 1.0)    │
    └────────────────────────────────────────────────────────────────────┘
```

When a request arrives for an index, the routing pipeline interacts with the index slot at three points:

```
    Request: POST /orders/_search
                    │
                    ▼
    ┌────────────────────────────────────────┐
    │  1. SLOT LOOKUP                        │
    │     slot = cache.getOrCreate("orders") │
    │     slot.requestDecay += 1.0           │  ◄── volume tracking
    │     K = effectiveFanOut(slot)          │  ◄── how many candidates?
    │     shardNodes = slot.shardNodeNames   │  ◄── who has shard data?
    └──────────────┬─────────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────┐
    │  2. CANDIDATE SELECTION              │
    │     candidates = rendezvousTopK(     │
    │       "orders", conns, K, shardNodes)│  ◄── consistent K-node subset
    └──────────────┬───────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────┐
    │  3. SCORING                          │
    │     for each candidate:              │
    │       info = slot.shardNodeInfo(     │
    │         candidate.Name)              │  ◄── primary/replica counts
    │       cwnd = candidate.loadCwnd(     │
    │         poolName)                    │  ◄── congestion window
    │       inFlight = candidate.          │
    │         loadInFlight(poolName)       │  ◄── in-flight count
    │       score = rttBucket              │
    │             * (inFlight + 1) / cwnd  │
    │             * shardCost(info)        │
    │     candidate with lowest score wins │
    └──────────────────────────────────────┘
```

### Rendezvous Hashing

Candidate selection uses rendezvous (highest random weight) hashing to produce consistent per-index node assignments:

1. **Partition** the connection list: shard-hosting nodes first, others after.
2. **Fill** the candidate set up to fan-out K, ordered by RTT tier, hash within tier.
3. **Rotate** by a jitter offset to spread traffic across candidates.

When `n_s >= K`, all K candidates are shard hosts. When `n_s < K`, the remaining slots are filled from non-shard nodes (penalized 32x by the cost table).

Rendezvous hashing provides two properties that round-robin lacks:

- **Consistency**: the same index reliably maps to the same candidate set, maximizing OS page-cache hit rates.
- **Minimal disruption**: when a node is added or removed, only 1/K of the index-to-node mappings change (versus potentially all mappings under round-robin).

#### Tier-by-Tier Slot Filling

The slot-filling algorithm constructs a fan-out set of K nodes, filling shard nodes first, ordered by RTT tier (nearest first). When more nodes exist in a tier than remaining slots, rendezvous hashing selects the winners:

```
    Building a K=5 fan-out set from a 12-node cluster
    (2 shard nodes in AZ-1, 1 shard node in AZ-2, 9 non-shard nodes)

    Input connections (pre-sorted by RTT bucket):

    Shard partition (phase 1):
    ┌─────────────┬────────┬──────────────────────────────────────────────┐
    │ Node        │ Bucket │ Action                                       │
    ├─────────────┼────────┼──────────────────────────────────────────────┤
    │ n1 (shard)  │ 8      │ Tier bucket=8: 2 shard nodes, 2 < 5 remain   │
    │ n2 (shard)  │ 8      │   -> take both. Remaining: 5-2 = 3           │
    ├─────────────┼────────┼──────────────────────────────────────────────┤
    │ n3 (shard)  │ 11     │ Tier bucket=11: 1 shard node, 1 < 3 remain   │
    │             │        │   -> take it. Remaining: 3-1 = 2             │
    └─────────────┴────────┴──────────────────────────────────────────────┘
    Phase 1 used 3 slots (all shard nodes), 2 remaining.

    Non-shard partition (phase 2):
    ┌─────────────┬────────┬──────────────────────────────────────────────┐
    │ n4          │ 8      │ Tier bucket=8: 4 non-shard nodes, 4 > 2      │
    │ n5          │ 8      │   -> rank by rendezvous hash, take top 2.    │
    │ n6          │ 8      │   -> n5, n7 win (highest hash weights).      │
    │ n7          │ 8      │   Remaining: 0. Done.                        │
    └─────────────┴────────┴──────────────────────────────────────────────┘

    Final fan-out set (5 slots):
    ┌──────┬──────┬──────┬──────┬──────┐
    │  n1  │  n2  │  n3  │  n5  │  n7  │
    │ b=8  │ b=8  │ b=11 │ b=8  │ b=8  │
    │shard │shard │shard │proxy │proxy │
    └──────┴──────┴──────┴──────┴──────┘
      ▲ nearest-first, shard-first
```

### Fan-Out Sizing

The fan-out K is automatically derived per-index from request volume and shard placement. It adapts over time:

- Low-traffic indexes: K starts small (2-3), concentrating traffic for cache locality.
- High-traffic indexes: K grows to spread load, preventing hotspots.
- Indexes where all nodes host shards: K equals the pool size (effectively weighted round-robin within shard hosts).

The effective fan-out is clamped between a hard floor and a hard ceiling. Within those bounds, three inputs compete via `max()`:

```
    Fan-out bounds for an index with 5 shard nodes on a 32-node cluster
    (minFanOut=1, maxFanOut=32)

    fan-out
     32 │─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ maxFanOut (config ceiling)
        │                                 ╭───────
        │                            ╭────╯
     24 │                        ╭───╯
        │                    ╭───╯
        │                ╭───╯
     16 │            ╭───╯
        │        ╭───╯ rateFanOut = int(counter/500) + 1
        │    ╭───╯       (grows with request volume)
      8 │╭───╯
        │╯
      5 │━━━━━━━━━━━━━━━━━━ shardNodeCount (from /_cat/shards)
        │
      1 │── minFanOut (config floor)
      0 └──────────────────────────────────────────────────── request rate
         idle        low        moderate       high       saturated


    Three inputs compete:

    ┌─────────────────┬──────────────┬──────────────────────────────────────┐
    │ Input           │ Source       │ Purpose                              │
    ├─────────────────┼──────────────┼──────────────────────────────────────┤
    │ minFanOut       │ Config       │ Absolute floor (default: 1)          │
    │ shardNodeCount  │ /_cat/shards │ Cover all nodes with shard data      │
    │ rateFanOut      │ Decay counter│ Scale with request volume            │
    └─────────────────┴──────────────┴──────────────────────────────────────┘

    Fan-out = max(minFanOut, shardNodeCount, rateFanOut)
    Clamped to: min(maxFanOut, activeNodeCount)
```

Fan-out adapts over time as traffic patterns change:

```
    Fan-out lifecycle for a hot index (5 shard nodes, 32-node cluster)

    fan-out
     32 │─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ maxFanOut
        │
        │            ╭────────╮
     24 │        ╭───╯        ╰────╮
        │    ╭───╯ peak traffic    ╰───╮
     16 │╭───╯                         ╰───╮
        │                                  ╰───╮
      8 │                                      ╰───╮
      5 │━━━━╮                                     ╰━━━━━ shard floor
        │    ╰───── ramp-up                    decay ────╯
      1 │
      0 └───────────────────────────────────────────────────── time
        t0          t1          t2          t3          t4

    t0: Index created, first request. Fan-out = max(1, 5, 1) = 5.
    t1: Traffic ramps. Decay counter rises, rateFanOut > 5. Fan-out grows.
    t2: Peak traffic. Fan-out hits maxFanOut ceiling (32). Clamped.
    t3: Traffic subsides. Counter decays, rateFanOut shrinks. Fan-out contracts.
    t4: Idle. Counter < 1.0. Fan-out returns to shard floor (5).
        If idle > 90 min, the slot is evicted entirely.
```

---

## 6. Shard-Exact Routing

For document-level operations (`GET /index/_doc/id`, `PUT /index/_doc/id`), the client can compute the exact shard:

```
GET /my-index/_doc/abc123
  │
  v
extractDocumentFromPath() → ("my-index", "abc123")
  │
  v
effectiveRoutingKey = "abc123"  (or ?routing= value if present)
  │
  v
murmur3("abc123") → shard N
  │
  v
lookup shard N → [primary: node2, replica: node5]
  │
  v
connScoreSelect([node2, node5], shardCostForReads, "get")
  │
  v
return best node
```

This matches the server's `OperationRouting.generateShardId()` algorithm. The client scores only the 1-3 nodes hosting that exact shard, bypassing the rendezvous hash fan-out entirely. The request hits a node with the data in its OS page cache.

When the shard map is not yet available (first seconds after startup), the path falls back to rendezvous hashing.

---

## 7. Thread Pool Congestion Control

### AIMD Congestion Windows

Each connection tracks a per-thread-pool congestion window (cwnd) using TCP-style AIMD (Additive Increase, Multiplicative Decrease). The ceiling comes from the server's actual thread pool configuration, discovered via `/_nodes/_local/stats`:

```
cwnd
 13 │                              ●━━━━━━━━━━━━━━━━━━  maxCwnd (search pool size)
    │                           ╱
  8 │                       ●╱       slow start: cwnd *= 2
    │                    ╱
  4 │                ●╱
    │             ╱
  2 │         ●╱
    │      ╱
  1 │  ●╱
    └──┬──────┬──────┬──────┬──────┬──── poll cycles (5s each)
       0      1      2      3      4
                                   └── steady state in ~20s
```

### State Machine

The AIMD controller runs on each stats poll cycle, per pool, per node:

```
┌─ Rejections increased? ── YES ──→ cwnd /= 2, set overloaded flag
│                                    (node skipped for this pool)
│
└─ NO ──→ Congestion signal?
           │
           ├─ RESIZABLE pools (search): wait_per_completed >= 1ms?
           │
           └─ FIXED pools (write, get): queue > 0 AND active >= max?
           │
           ├─ Congested ──→ cwnd /= 2        (multiplicative decrease)
           │
           └─ Clear ──→ cwnd < ssthresh?
                        ├─ YES ──→ cwnd *= 2  (slow start)
                        └─ NO  ──→ cwnd += 1  (congestion avoidance)
```

### Per-Thread Pool Congestion Structure

Each connection tracks congestion state separately for each OpenSearch thread pool:

```
    poolCongestion per pool per connection
    ┌────────────────────────────────────────────────────────────────────┐
    │  cwnd: atomic.Int32          Current congestion window (>= 1)      │
    │  inFlight: atomic.Int32      Client-tracked in-flight requests     │
    │  overloaded: atomic.Bool     Set by stats poller or 429 response   │
    │                                                                    │
    │  mu (stats poller writes):                                         │
    │    maxCwnd: int32            Ceiling from thread pool config       │
    │    ssthresh: int32           Slow-start threshold                  │
    │    prevCompleted: int64      Previous completed count (delta)      │
    │    prevRejected: int64       Previous rejected count (delta)       │
    │    prevWaitTimeNano: int64   Previous wait time (delta, RESIZABLE) │
    │    hasWaitTime: bool         Pool reports total_wait_time_in_nanos │
    └────────────────────────────────────────────────────────────────────┘
```

Each connection holds a `poolRegistry` -- a `sync.Map` of pool name to congestion state:

```
    Connection.pools (poolRegistry)
    ┌─────────────────────────────────────────────────────┐
    │  pools: sync.Map                                    │
    │    "search" -> poolCongestion{cwnd=13, inFlight=3}  │
    │    "write"  -> poolCongestion{cwnd=9,  inFlight=0}  │
    │    "get"    -> poolCongestion{cwnd=9,  inFlight=1}  │
    │                                                     │
    │  defaultPool: poolCongestion{cwnd=32, inFlight=2}   │
    │    (4 * allocatedProcessors; for unmapped routes)   │
    └─────────────────────────────────────────────────────┘
```

### Why Per-Pool

OpenSearch maintains separate thread pools for search, write, get, management, and other operation types. A node can be overloaded for writes (full bulk queue) while having ample search capacity. Per-pool tracking lets the scoring formula reflect this: a node with a saturated write pool scores `MaxFloat64` for writes but remains eligible for searches at its normal score.

---

## 8. Connection Pool Lifecycle

### Three-Partition Structure

Every policy pool manages connections in three partitions:

```
multiServerPool
┌──────────────────────────────────────────────────────────────┐
│  ready[]           (single slice, two logical partitions)    │
│  ┌────────────────────────┬───────────────────────────┐      │
│  │       active           │         standby           │      │
│  │  ready[0:activeCount]  │  ready[activeCount:len]   │      │
│  │  Round-robin selection │  Idle, health-checked     │      │
│  │  Serves production     │  before promotion         │      │
│  └────────────────────────┴───────────────────────────┘      │
│                                                              │
│  dead[]            (separate slice)                          │
│  ┌───────────────────────────────────────────────────────┐   │
│  │  Failed connections, exponential-backoff resurrection │   │
│  └───────────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────────┘
```

Selection priority when `Next()` is called:

1. **Active** -- round-robin, warmup-aware.
2. **Standby** -- emergency promotion, no warmup.
3. **Dead (zombie fallback)** -- rotate through dead list one at a time.
4. **Error** -- `ErrNoConnections`.

### Connection State Word

Each connection's full state is packed into a single atomic 64-bit word, enabling lock-free reads and CAS-based updates:

```
63              52 51                      26 25                       0
┌────────────────┬──────────────────────────┬──────────────────────────┐
│   lifecycle    │     warmupConfig         │      warmupState         │
│   (12 bits)    │     (26 bits)            │      (26 bits)           │
└────────────────┴──────────────────────────┴──────────────────────────┘
```

The lifecycle bits encode readiness, position, and metadata flags:

| Bit   | Name               | Meaning                               |
| ----- | ------------------ | ------------------------------------- |
| 0x01  | `lcReady`          | Connection believed functional        |
| 0x02  | `lcUnknown`        | Status uncertain, needs health check  |
| 0x04  | `lcActive`         | In active partition, serving requests |
| 0x08  | `lcStandby`        | In standby partition, idle            |
| 0x10  | `lcNeedsWarmup`    | Needs warmup before full traffic      |
| 0x20  | `lcOverloaded`     | Node under excessive load, parked     |
| 0x40  | `lcHealthChecking` | Health check goroutine running        |
| 0x80  | `lcDraining`       | HTTP/2 GOAWAY received                |
| 0x100 | `lcNeedsHardware`  | Needs `/_nodes/_local/http,os` call   |
| 0x200 | `lcNeedsCatUpdate` | Shard placement stale                 |

When neither position bit (`lcActive` | `lcStandby`) is set, the connection is on the dead list.

### Connection Lifecycle Transitions

The lifecycle state machine governs how connections move between partitions:

```
                         ┌─────────────┐
                         │  Discovery  │
                         │   (new)     │
                         └──────┬──────┘
                                │
                                ▼
          ┌──────────────────────────────────────────────┐
          │  lcUnknown | lcNeedsWarmup | lcNeedsHardware │
          │  (initial state for new conns)               │
          └──────────────┬──────────────────┬────────────┘
                         │                  │
              health check               health check
                passed                     failed
                         │                  │
                         ▼                  ▼
    ┌────────────────────────┐    ┌────────────────────────────────┐
    │  lcActive|lcNeedsWarmup│    │  lcUnknown | lcNeedsHardware   │
    │  (active + warming up) │    │  (dead list, hardware unknown) │
    └───────────┬────────────┘    └────────┬───────────────────────┘
                │                          │
         warmup completes          scheduleResurrect
                │                          │
                ▼                          │
    ┌────────────────────────┐             │
    │  lcReady | lcActive    │◄────────────┘
    │  (fully active)        │     resurrection
    └─────────┬──────────────┘
              │
              │ OnFailure()
              │
              ▼
    ┌──────────────────────────────┐
    │  lcUnknown | lcNeedsHardware │
    │  (dead, hardware re-check)   │
    └──────────────────────────────┘

              │ overload detected
              │ (stats poller)
              ▼
    ┌─────────────────────────────┐
    │ lcStandby|lcOverloaded      │
    │ (standby, not failed --     │
    │  stats poller clears flag   │
    │  when metrics improve)      │
    └─────────────────────────────┘
```

When a connection transitions to dead via `OnFailure()`, `lcNeedsHardware` is set so that hardware info is re-verified on resurrection -- the node may have been replaced with different hardware during the outage.

### Warmup

Resurrected and newly-promoted connections go through a non-linear warmup ramp using a smoothstep (Hermite) curve:

```
skip = maxSkip × (1 - 3t² + 2t³)     where t = roundsElapsed / maxRounds
```

This produces an S-shaped acceptance ramp designed around the JVM HotSpot JIT compiler:

```
R16 ████████████████████████████████ 32    3% ╮ slow soak:
R13 █████████████████████████████    29    3% ╯ JVM interprets cold bytecode
R10 █████████████████████            21    5% ╮ accelerating:
R7  █████████████                    13    7% ╯ C1/C2 JIT compiles hot methods
R4  █████                             5   17% ╮ decelerating:
R1                                    0  100% ╯ smooth arrival at full traffic
```

The slow initial trickle allows the JVM's C1 and C2 compilers to profile and compile hot code paths -- search execution, bulk indexing, aggregation pipelines -- without subjecting the node to full production load while running unoptimized bytecode.

### Standby Rotation

The discovery loop periodically rotates standby connections to keep scoring data (RTT, congestion windows) fresh:

```
t=0s   [node1 ●] [node2 ●] [node3 ○]    ● = active  ○ = standby
t=15s  [node1 ○] [node2 ●] [node3 ●]    node3 promoted (with warmup)
t=30s  [node1 ●] [node2 ○] [node3 ●]    node1 promoted back
t=45s  [node1 ●] [node2 ●] [node3 ○]    full cycle, all data refreshed
```

Cycle time: `N × discoveryInterval`. On a 3-node cluster with 15s discovery, each node spends ~15s on standby before rotation.

### Weighted Round-Robin

For heterogeneous clusters where nodes have different core counts, the client distributes traffic proportionally:

```
Cluster cores:  [8, 16, 24]  → GCD=8 → weights: [1, 2, 3]
```

Connections are duplicated in `ready[]` by weight, preserving O(1) selection (modular arithmetic on an atomic counter). Hardware info is obtained via `/_nodes/http,os,thread_pool` during discovery.

---

## 9. Health Checking and Load Shedding

### Two-Phase Health Checks

The client uses a capability-detection approach:

1. **Phase 1**: `GET /` confirms HTTP connectivity and parses the OpenSearch version.
2. **Phase 2** (if permitted): `GET /_cluster/health?local=true` checks shard initialization status.

If `initializing_shards > 0`, the node remains in the dead list and is retried next cycle. This prevents routing traffic to a node still absorbing shard data after a restart.

The cluster health endpoint requires `cluster:monitor/health` permission. The client probes asynchronously and falls back to `GET /` when the permission is unavailable, with periodic re-probes (default 4h) in case the permission is granted later.

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

Transient errors (5xx, network timeout) keep the state at Pending and retry on the next health check cycle.

### Load Shedding via Node Stats

A background poller calls `/_nodes/_local/stats/jvm,breaker,thread_pool` on each live node:

| Condition                                     | Action                        |
| --------------------------------------------- | ----------------------------- |
| JVM `heap_used_percent` >= 85%                | `demoteOverloaded(conn)`      |
| Any circuit breaker `estimated/limit` >= 0.90 | `demoteOverloaded(conn)`      |
| Any circuit breaker `tripped` count increased | `demoteOverloaded(conn)`      |
| Cluster health status `"red"`                 | `demoteOverloaded(conn)`      |
| All metrics clear                             | `promoteFromOverloaded(conn)` |

Overloaded connections move from active to standby with `lcOverloaded` set. Unlike failures, the connection is healthy -- it's under excessive load. The stats poller clears the flag when metrics improve.

### Background Goroutines

| Goroutine                      | Interval            | HTTP Calls                     | Purpose                            |
| ------------------------------ | ------------------- | ------------------------------ | ---------------------------------- |
| `discoveryLoop`                | 5m (configurable)   | `/_nodes/http`, `/_cat/shards` | Topology + shard placement refresh |
| `scheduleNodeStats`            | Auto 5-30s          | `/_nodes/_local/stats`         | Load shedding, CWND updates        |
| `scheduleClusterHealthRefresh` | Auto 5s-5m          | `/_cluster/health?local=true`  | Readiness gate, cluster status     |
| `scheduleResurrect` (per conn) | Exponential backoff | `GET /`                        | Dead connection recovery           |

All goroutines derive a child context from `client.ctx`, so `Close()` cancels them all.

---

## 10. Resurrection and Backoff

When a node fails, the client schedules resurrection with cluster-aware exponential backoff. Three competing inputs determine the retry interval; the largest wins:

```
finalTimeout = max(healthTimeout, rateLimitedTimeout, minimumFloor) + jitter
```

| Input                | Formula                                                    | Purpose                        |
| -------------------- | ---------------------------------------------------------- | ------------------------------ |
| Health-ratio timeout | `initial × 2^(failures-1) × (liveNodes/totalNodes)`        | Healthy clusters wait longer   |
| Rate-limited timeout | `(liveNodes × clientsPerServer) / serverMaxNewConnsPerSec` | Throttles TLS handshake storms |
| Minimum floor        | 500ms                                                      | Absolute lower bound           |
|                      |                                                            |                                |

The capacity model values (`clientsPerServer=8`, `serverMaxNewConnsPerSec=32`) are auto-derived from the server's core count.

### Recovery Timeline: 150-Node Cluster

```
Live  Dead  Timeout  Behavior
────  ────  ───────  ────────────────────────────
  0   150   500ms    All dead: most aggressive
 10   140   2.5s     Rate limit: (10×8)/32
 50   100   12.5s    Rate limit dominates
100    50   25s      Rate limit: (100×8)/32
149     1   30s      Nearly healthy: most conservative
```

**Key property**: the client is most aggressive when all servers are down (500 ms retries to recover capacity quickly) and most conservative when the cluster is nearly healthy (30 s retries to avoid TLS handshake storms on recovering servers).

```
    Recovery timeline (150-node cluster, all nodes fail then recover together):

    Timeout
    (seconds)
    30 |                                              .............
       |                                         ....
       |                                     ...
       |                                  ..
    20 |                               ..
       |                            ..
       |                          .
       |                        .
    15 |                      .  <- rate limit: (live * 8) / 32
       |                    .
       |                  .
    10 |                .
       |              .
       |            .
       |          .
     5 |        .
       |      .
       |    .
       |  .
     1 | .
    0.5|x  <- minimum floor (all dead, most aggressive)
       +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+-
       0  10 20 30 40 50 60 70 80 90 100   120   140 150
                        Live Nodes ->
```

**Why rate limit uses live nodes (not dead nodes)**: dead servers are unreachable -- retrying them faster creates no load. The bottleneck is the _recovering_ servers: as more servers come back, every client reconnects to every recovering server simultaneously. TLS handshake storms can overload recovering nodes before they finish starting up. The rate limit grows with `liveNodes` to account for this increasing pressure.

### Failure Response Timeline

```
t=0s    Request fails → OnFailure(conn) → conn: Active → Dead
        scheduleResurrect spawned
        conn.setNeedsCatUpdate() → excluded from scored routing
        requestCatRefresh() → atomic flag for discovery loop

t=5s    Cat-only refresh: /_cat/shards → scored routing resumes for survivors

t=5-30s scheduleResurrect backoff: exponential + rate-limited

t=Xs    Health check passes → resurrectWithLock() → Dead → Active (with warmup)

t=5m    Full discovery cycle → topology + shard placement refresh
```

During the gap, remaining active connections serve traffic. If all are exhausted, standby provides emergency capacity. If standby is empty, zombie fallback rotates through dead connections until recovery.

---

## 11. Error Handling and Partial Failures

### OpenSearch's Partial Success Model

HTTP 2xx does **not** guarantee complete success. OpenSearch returns partial results rather than failing entirely:

| Operation           | HTTP Status | Partial Failure Indicator | Impact                                     |
| ------------------- | ----------- | ------------------------- | ------------------------------------------ |
| Bulk                | 200         | `errors: true`            | Data loss -- some documents not indexed    |
| Search              | 200         | `_shards.failed > 0`      | Incomplete results -- missing data         |
| Index/Update/Delete | 201/200     | `_shards.failed > 0`      | Durability risk -- no replica confirmation |
| Refresh             | 200         | `_shards.failed > 0`      | Incomplete refresh                         |

Applications must always check these indicators. The Go client returns typed response structs that expose these fields directly.

### Bulk Timeout Interaction

Retrying bulk requests after `context.DeadlineExceeded` is safe **only** when the original request included a server-side timeout (`BulkParams.Timeout`). Without one, the server continues processing after the client gives up. Each retry adds concurrent bulk operations to the same primary shards, potentially exhausting thread pools cluster-wide.

Rules for safe bulk retry:

1. Always set `BulkParams.Timeout` shorter than the client-side context deadline.
2. Do not retry on `context.DeadlineExceeded` without a server-side timeout.
3. Retry only failed items, not the entire batch.
4. Use client-assigned `_id` values so you can query which items persisted before retrying.

---

## 12. Cost and Efficiency Model

### Notation

| Symbol | Meaning                                |
| ------ | -------------------------------------- |
| N      | Total cluster nodes                    |
| n_s    | Nodes hosting shards for a given index |
| K      | Fan-out (candidate set size)           |
| P      | Primary shards for an index            |
| R      | Replication factor                     |
| d      | Average response payload size          |
| Q      | Request rate (req/s)                   |
| A      | Number of availability zones           |
| eta    | Hop elimination rate: `1 - n_s/N`      |

### Coordinator Hop Elimination

The single most important metric. The fraction of requests eliminated from the coordinator proxy path:

```
eta = 1 - n_s / N
```

| Cluster  | Index (example)   | n_s | P(proxy) Round-Robin | P(proxy) Scored | eta  |
| -------- | ----------------- | --- | -------------------- | --------------- | ---- |
| 6-node   | products (1P/1R)  | 2   | 67%                  | ~0%             | 0.67 |
| 32-node  | users (1P/1R)     | 2   | 94%                  | ~0%             | 0.94 |
| 32-node  | events (8P/1R)    | 14  | 56%                  | ~0%             | 0.56 |
| 256-node | reference (1P/1R) | 2   | 99%                  | ~0%             | 0.99 |
| 256-node | logs (32P/1R)     | 50  | 80%                  | ~0%             | 0.80 |

**Measured**: On a 3-node cluster with a 0-replica index (n_s=1), scored routing directed 100% of 60,000 requests to the single shard-holding node at 320 req/s. Round-robin would have proxied 67%.

### Availability Improvement

Each serial link is a failure domain with independent availability p:

```
A_proxied = p³     (3 links: client↔coordinator, coordinator↔shard)
A_direct  = p²     (2 links: client↔shard)

deltaA = p² × eta × (1 - p)
```

| Cluster  | Index     | eta  | A_old    | A_new    | deltaA    |
| -------- | --------- | ---- | -------- | -------- | --------- |
| 6-node   | products  | 0.67 | 0.997670 | 0.998001 | +0.000331 |
| 32-node  | users     | 0.94 | 0.997065 | 0.998001 | +0.000936 |
| 256-node | reference | 0.99 | 0.997011 | 0.998001 | +0.000990 |

### Blast Radius Reduction

Under round-robin, a partition between any two cluster nodes can affect any index:

```
Links at risk (round-robin): N(N-1)/2    (full mesh)
Links at risk (scored):      n_s          (only shard-hosting nodes)
```

| Cluster  | Full Mesh Links | n_s=2 Index | Reduction |
| -------- | --------------- | ----------- | --------- |
| 6-node   | 15              | 2           | 7.5x      |
| 32-node  | 496             | 2           | 248x      |
| 256-node | 32,640          | 2           | 16,320x   |
|          |                 |             |           |

A network partition between two nodes that don't host the target index has **zero impact** on that index's requests.

### Bandwidth Savings

Each proxied request carries the response across two extra internal links:

```
BW_saved = Q × eta × 2d
```

**32-node cluster** (50K req/s, d=8 KiB, eta=0.80):

```
Internal BW saved = 50,000 × 0.80 × 2 × 8 KiB = 625 MiB/s
```

### JVM Heap Savings

The coordinator buffers the full response in JVM heap:

```
Heap freed = concurrent_requests × eta × d
```

**2,000 concurrent requests** (d=16 KiB, eta=0.85):

```
Coordinator heap freed = 2,000 × 0.85 × 16 KiB = 27.2 MiB
```

For aggregation responses (100 KiB-1 MiB), scale proportionally. This directly reduces GC pressure and circuit-breaker utilization.

### Page Cache Concentration

Under round-robin, coordinator proxying pulls response payloads through all nodes, fragmenting the page cache across all indexes. With affinity, a node only handles requests for its local shards.

**Example**: 64 GiB RAM, 32 GiB JVM heap = 32 GiB page cache. Node hosts 30 indexes, 200 GiB shard data. Cluster has 600 indexes.

```
                    Round-Robin             Affinity
────────────────── ─────────────────────── ───────────────────────────
Indexes cached      ~600 (all, shallow)     ~30 (local, deep)
Cache per index     32 GiB / 600 = 55 MiB   32 GiB / 30 = 1.07 GiB
Improvement         ─                       19× per-index cache depth
```

### Cross-AZ Hop Reduction

Under round-robin, the probability a coordinator is in a **different** AZ from the shard host:

```
P(cross_AZ) = (A - 1) / A
```

For 3 AZs: P(cross_AZ) = 2/3 — two-thirds of coordinator proxy hops cross an AZ boundary.

With affinity routing, the fraction of cross-AZ proxy hops eliminated is:

```
cross_AZ_hops_eliminated = eta × P(cross_AZ)
```

For a 32-node cluster with a typical index on 7 nodes across 3 AZs:

```
eta = 1 - 7/32 = 0.78
cross-AZ hops eliminated = 0.78 × 0.667 = 52% of all requests
```

The bandwidth reduction is proportional to response size. Bulk write payloads (5–15 MiB) are 100–1000× larger than search responses, so each eliminated cross-AZ hop on the write path removes proportionally more network transfer.

### Client-Side Memory Cost

| Component               | Size       | Notes                                       |
| ----------------------- | ---------- | ------------------------------------------- |
| Per-index slot          | ~200-500 B | Name, fan-out, shard map, hash cache        |
| Per-connection overhead | ~120-300 B | RTT ring, pool registry, in-flight counters |

| Cluster  | Nodes | Indexes | Total Client RAM |
| -------- | ----- | ------- | ---------------- |
| 6-node   | 6     | 20      | ~9 KiB           |
| 32-node  | 32    | 100     | ~44 KiB          |
| 256-node | 256   | 500     | ~230 KiB         |

Even on a 256-node cluster with 500 indexes, the total is well under 1 MiB.

### Shard Consolidation Synergy

Affinity routing amplifies the benefits of right-sized shards. A 1.5 TiB index:

| Max Shard Size   | Primaries | Shard Nodes | eta  | Scatter Messages |
| ---------------- | --------- | ----------- | ---- | ---------------- |
| 34 GiB (typical) | 45        | ~60         | 0.70 | 90               |
| 100 GiB          | 15        | ~24         | 0.88 | 30               |
| 200 GiB          | 8         | ~14         | 0.93 | 16               |

45→8 primaries eliminates 82% of scatter-gather overhead. For aggregation queries, per-shard fixed costs (segment reader init, field data loading, doc values memory-mapping, partial result serialization) are frequently the dominant cost.

---

## 13. Operational Guide

### Enabling Request Routing

```go
import "github.com/opensearch-project/opensearch-go/v4/opensearchtransport"

router := opensearchtransport.NewDefaultRouter()

client, err := opensearch.NewClient(opensearch.Config{
    Addresses:             []string{"https://node1:9200", "https://node2:9200"},
    DiscoverNodesOnStart:  true,
    DiscoverNodesInterval: 5 * time.Minute,
    Router:                router,
})
```

With no additional configuration, this enables the full scoring pipeline. Without `Router`, the client uses pre-existing round-robin behavior.

### Pre-Built Routers

| Router      | Constructor             | Use Case                                                                               |
| ----------- | ----------------------- | -------------------------------------------------------------------------------------- |
| Default     | `NewDefaultRouter()`    | Role-based + per-index connection scoring with RTT, congestion, and shard cost.        |
| Mux         | `NewMuxRouter()`        | Role-based routing without connection scoring. Useful for debugging routing decisions. |
| Round-Robin | `NewRoundRobinRouter()` | Coordinating-only preference with round-robin fallback. Simplest option.               |

```go
// Role-based routing without connection scoring (useful for comparing behavior)
router := opensearchtransport.NewMuxRouter()

// Simple coordinating-only preference with round-robin fallback
router := opensearchtransport.NewRoundRobinRouter()
```

All three routers share the same policy chain structure:

1. **Coordinating-only nodes** (if any exist, routes all traffic to them exclusively)
2. **Operation-specific routing** (Default/Mux only: bulk/reindex->ingest, search/msearch/count/scroll/PIT/validate/rank_eval->search/data, get/mget/termvectors->search/data, refresh/flush/forcemerge->data, stats/recovery/shard_stores->data, etc.)
3. **Round-robin fallback** (high availability)

### Custom Routers

The pre-built routers cover most use cases. Custom routers are useful for non-standard policy composition.

#### Policy Primitives

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

// Null: always returns (NextHop{}, nil) -- used as a fallthrough terminator
opensearchtransport.NewNullPolicy()
```

#### Role Preference with Fallback

Route to data nodes when available, otherwise round-robin:

```go
dataPolicy, _ := opensearchtransport.NewRolePolicy("data")
router := opensearchtransport.NewRouter(
    dataPolicy,
    opensearchtransport.NewRoundRobinPolicy(),
)
```

#### Composing Policies Manually

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

#### Conditional Routing

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

### Cluster Topology Examples

**Mixed cluster with coordinating-only nodes:**

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

With `NewDefaultRouter()`:

- All requests -> coordinator-1 (coordinating-only nodes get exclusive traffic)

With `NewMuxRouter()` (if no coordinating-only nodes):

- `POST /_bulk` -> ingest-1
- `POST /my-index/_search` -> data-1
- `POST /my-index/_refresh` -> data-1
- `GET /_cluster/health` -> round-robin across all non-manager nodes

**With search nodes (OpenSearch 3.0+):**

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

With `NewDefaultRouter()`:

- `POST /_bulk` -> ingest-1 (with connection scoring)
- `POST /products/_search` -> search-1 (with per-index scoring)
- `GET /products/_doc/123` -> search-1 (with document-level scoring)
- `POST /products/_search/scroll` -> search-1 (with per-index scoring)
- `POST /products/_refresh` -> data-1 or data-2 (shard maintenance targets data nodes)

If search-1 is removed, search operations automatically fall back to data nodes.

**Single node (development):**

- All policies match the same node
- No performance difference vs round-robin
- Same code works in development and multi-node clusters

### Health Check Configuration

For details on the health check endpoint -- response fields, HTTP status codes, required permissions, and security configuration -- see [cluster_health_checking.md](cluster_health_checking.md).

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

### Node Stats Polling and Load Shedding

A background goroutine polls each live node's JVM heap usage and circuit-breaker metrics to detect overloaded nodes and shed load away from them. This is enabled by default with automatically derived polling intervals.

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

### Observability

The `ConnectionObserver` interface provides 14 callbacks:

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

```go
type myObserver struct {
    opensearchtransport.BaseConnectionObserver
}

func (o *myObserver) OnRoute(event opensearchtransport.RouteEvent) {
    log.Printf("routed %s %s to %s (score=%.2f)",
        event.Method, event.Path, event.WinnerURL, event.WinnerScore)
}
```

Observer methods may be called while internal locks are held. Implementations must not call back into the client.

### Debugging

Set `OPENSEARCH_GO_DEBUG=true` to see policy paths, scoring decisions, and override actions logged to stderr.

### Emergency Overrides

Operators can disable specific routing policies at runtime without code changes:

```bash
# Disable all role policies (fall through to round-robin)
OPENSEARCH_GO_POLICY_ROLE=false

# Disable connection scoring (plain role-based routing)
OPENSEARCH_GO_POLICY_ROUTER=false

# Disable shard-exact routing
OPENSEARCH_GO_ROUTING_CONFIG=-shard_exact

# Skip shard catalog discovery
OPENSEARCH_GO_DISCOVERY_CONFIG=-cat_shards
```

### Shutdown

`Close()` cancels all background goroutines (discovery, stats polling, health checks, resurrection) and closes idle HTTP connections:

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

The caller is responsible for stopping new requests before calling `Close()`. In-flight requests complete (or fail) normally against the underlying `http.Transport`.

In tests, pass `t.Context()` as `Config.Context` so that all background goroutines are automatically cancelled when the test ends.

### Coordinating Tier Reduction

Request routing reduces the fraction of requests that require coordinator proxying. The hop elimination rate for a given index is `eta = 1 - n_s / N` where `n_s` is the number of nodes hosting shards for that index and `N` is the total cluster size (see [§12](#12-cost-and-efficiency-model)). For clusters with dedicated coordinating-only nodes, this reduction in proxy load may justify shrinking or eliminating the coordinator tier.

---

## 14. Configuration Reference

### Client Configuration

| Setting                   | Default      | Env Override                              | Description                                            |
| ------------------------- | ------------ | ----------------------------------------- | ------------------------------------------------------ |
| `RequestTimeout`          | 0 (none)     | `OPENSEARCH_GO_REQUEST_TIMEOUT`           | Per-attempt timeout for each HTTP round-trip           |
| `DiscoverNodesInterval`   | 5m           | --                                        | Full topology + shard refresh interval                 |
| `HealthCheckTimeout`      | 5s           | --                                        | Per-request health check timeout                       |
| `ResurrectTimeoutInitial` | 5s           | --                                        | Starting backoff for dead connections                  |
| `ResurrectTimeoutMax`     | 30s          | --                                        | Cap before jitter                                      |
| `MinimumResurrectTimeout` | 500ms        | --                                        | Absolute floor                                         |
| `JitterScale`             | 0.5          | --                                        | Jitter multiplier for resurrection                     |
| `MaxRetryClusterHealth`   | 4h           | --                                        | Retry interval for unavailable cluster health endpoint |
| `NodeStatsInterval`       | auto (5-30s) | `OPENSEARCH_GO_NODE_STATS_INTERVAL`       | Stats polling interval                                 |
| `OverloadedHeapThreshold` | 85%          | `OPENSEARCH_GO_OVERLOADED_HEAP_THRESHOLD` | JVM heap threshold                                     |
| `OverloadedBreakerRatio`  | 0.90         | `OPENSEARCH_GO_OVERLOADED_BREAKER_RATIO`  | Breaker ratio threshold                                |

### Router Options

`NewDefaultRouter()` accepts functional options for tuning fan-out and index slot behavior:

```go
router := opensearchtransport.NewDefaultRouter(
    opensearchtransport.WithMinFanOut(3),                    // Minimum nodes per index slot
    opensearchtransport.WithMaxFanOut(10),                   // Maximum nodes per index slot
    opensearchtransport.WithDecayFactor(0.999),              // Decay factor for fan-out counters
    opensearchtransport.WithFanOutPerRequest(500),           // Decay-counter-to-fan-out divisor
    opensearchtransport.WithIdleEvictionTTL(90*time.Minute), // Idle index slot TTL
    opensearchtransport.WithIndexFanOut(map[string]int{      // Per-index fan-out overrides
        "hot-index": 8,
        "small-index": 2,
    }),
)
```

| Option                     | Default | Description                                                              |
| -------------------------- | ------- | ------------------------------------------------------------------------ |
| `WithMinFanOut(n)`         | 1       | Minimum nodes in an index slot. Floor for fan-out.                       |
| `WithMaxFanOut(n)`         | 32      | Maximum nodes in an index slot. Caps pathologically sharded indexes.     |
| `WithDecayFactor(d)`       | 0.999   | Fan-out counter decay factor. Must be in (0, 1). Higher = longer memory. |
| `WithFanOutPerRequest(f)`  | 500     | Decay counter value that maps to +1 fan-out node.                        |
| `WithIdleEvictionTTL(d)`   | 90 min  | How long an idle index slot persists before eviction.                    |
| `WithIndexFanOut(m)`       | nil     | Per-index fan-out overrides. Bypasses dynamic calculation.               |
| `WithShardExactRouting(b)` | true    | Enable/disable murmur3 shard-exact routing. Env var overrides.           |

### Feature Environment Variables

All `OPENSEARCH_GO_*` environment variables are evaluated once at client initialization and are immutable after. Environment variable settings override programmatic configuration values.

#### Debug and Diagnostics

| Variable              | Format | Default | Description                                                                |
| --------------------- | ------ | ------- | -------------------------------------------------------------------------- |
| `OPENSEARCH_GO_DEBUG` | Bool   | `false` | Enable debug logging to stderr for routing, discovery, and pool operations |

#### Routing and Discovery

| Variable                         | Format                          | Default       | Description                                                                                  |
| -------------------------------- | ------------------------------- | ------------- | -------------------------------------------------------------------------------------------- |
| `OPENSEARCH_GO_ROUTING_CONFIG`   | Comma-separated flags/key=value | (all enabled) | Toggle shard-exact routing (`-shard_exact`)                                                  |
| `OPENSEARCH_GO_DISCOVERY_CONFIG` | Comma-separated flags           | (all enabled) | Skip discovery calls: `-cat_shards`, `-routing_num_shards`, `-cluster_health`, `-node_stats` |
| `OPENSEARCH_GO_FALLBACK`         | Bool                            | `true`        | Seed URL fallback when all pools exhausted. `false` = disable                                |

`OPENSEARCH_GO_ROUTING_CONFIG` flags:

| Flag          | Default | `-` effect                                     |
| ------------- | ------- | ---------------------------------------------- |
| `shard_exact` | enabled | Disable murmur3 shard-exact connection routing |

`OPENSEARCH_GO_DISCOVERY_CONFIG` flags:

| Flag                 | Default | `-` effect                                                                 |
| -------------------- | ------- | -------------------------------------------------------------------------- |
| `cat_shards`         | enabled | Skip `GET /_cat/shards`. No shard placement data.                          |
| `routing_num_shards` | enabled | Skip `GET /_cluster/state/metadata`. Shard-exact falls back to rendezvous. |
| `cluster_health`     | enabled | Skip `GET /_cluster/health?local=true` probes.                             |
| `node_stats`         | enabled | Skip `GET /_nodes/_local/stats` polling.                                   |

Examples:

```bash
# Disable shard-exact routing
OPENSEARCH_GO_ROUTING_CONFIG=-shard_exact

# Skip metadata fetch and node stats (reduces server calls)
OPENSEARCH_GO_DISCOVERY_CONFIG=-routing_num_shards,-node_stats

# Minimal discovery: only node membership, no enrichment
OPENSEARCH_GO_DISCOVERY_CONFIG=-cat_shards,-routing_num_shards,-cluster_health,-node_stats
```

#### Load Shedding and Stats Polling

| Variable                                  | Format              | Default       | Description                                                      |
| ----------------------------------------- | ------------------- | ------------- | ---------------------------------------------------------------- |
| `OPENSEARCH_GO_NODE_STATS_INTERVAL`       | Duration or seconds | auto (5s-30s) | Stats polling interval. `0` or unset = auto. Negative = disabled |
| `OPENSEARCH_GO_OVERLOADED_HEAP_THRESHOLD` | Integer (0-100)     | `85`          | JVM heap threshold. `100` = disable heap detection               |
| `OPENSEARCH_GO_OVERLOADED_BREAKER_RATIO`  | Float (0.0-1.0)     | `0.90`        | Breaker ratio threshold. `1.0` = disable breaker detection       |

#### Connection Pool Tuning

| Variable                                  | Format              | Default | Description                                                             |
| ----------------------------------------- | ------------------- | ------- | ----------------------------------------------------------------------- |
| `OPENSEARCH_GO_ACTIVE_LIST_CAP`           | Integer             | auto    | Max active connections per pool. `0` or unset = auto-scale with cluster |
| `OPENSEARCH_GO_STANDBY_ROTATION_INTERVAL` | Duration or seconds | `30s`   | Interval between standby rotation cycles                                |
| `OPENSEARCH_GO_STANDBY_ROTATION_COUNT`    | Integer             | `1`     | Standby connections rotated per cycle                                   |
| `OPENSEARCH_GO_STANDBY_PROMOTION_CHECKS`  | Integer             | `3`     | Consecutive health checks before standby-to-active promotion            |

### Policy Override Variables

Each policy type has a corresponding environment variable:

| Variable                               | Policy Type      |
| -------------------------------------- | ---------------- |
| `OPENSEARCH_GO_POLICY_CHAIN`           | PolicyChain      |
| `OPENSEARCH_GO_POLICY_MUX`             | MuxPolicy        |
| `OPENSEARCH_GO_POLICY_IFENABLED`       | IfEnabledPolicy  |
| `OPENSEARCH_GO_POLICY_ROUTER`          | poolRouter       |
| `OPENSEARCH_GO_POLICY_ROLE`            | RolePolicy       |
| `OPENSEARCH_GO_POLICY_ROUNDROBIN`      | RoundRobinPolicy |
| `OPENSEARCH_GO_POLICY_INDEX_ROUTER`    | IndexRouter      |
| `OPENSEARCH_GO_POLICY_DOCUMENT_ROUTER` | DocRouter        |

#### Value Format

**Disable all instances of a type:**

```bash
# Disable all RolePolicy instances (falls through to round-robin)
OPENSEARCH_GO_POLICY_ROLE=false

# Disable connection scoring (falls back to plain role-based)
OPENSEARCH_GO_POLICY_ROUTER=false
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

Each policy in the tree gets a dot-delimited path with per-type sibling indices (e.g., `chain[0].ifenabled[0].chain[0].mux[0].role[0]`). Enable `OPENSEARCH_GO_DEBUG=true` to see policy paths and override actions logged to stderr.

Value parsing priority:

1. **Bool**: `strconv.ParseBool(value)` -- if the entire value is a valid bool, it applies to all instances. A value of `true` is a no-op (same as default).
2. **Path matchers**: Comma-separated `path=bool` items. The path portion is matched first as a regular expression, then as a string prefix.

When a policy is env-disabled:

- `IsEnabled()` returns `false`
- `Eval()` returns `(NextHop{}, nil)` (pass-through to next policy)
- `DiscoveryUpdate()` is a no-op on leaf policies (prevents accumulating connections)

The env override is applied once at startup, after policy configuration but before the first `DiscoveryUpdate`. It cannot be changed at runtime.

---

## Appendix A: Summary Formulas

| Metric                             | Formula                                     |
| ---------------------------------- | ------------------------------------------- |
| Hop elimination rate               | `eta = 1 - n_s / N`                         |
| Availability gain                  | `deltaA = p² × eta × (1 - p)`               |
| Bandwidth saved per request        | `2d × eta`                                  |
| Coordinator heap saved per request | `d × eta`                                   |
| Blast radius (links at risk)       | `n_s` (scored) vs `N(N-1)/2` (round-robin)  |
| Cross-AZ hops eliminated           | `eta × P(cross_AZ)`                         |
| Scatter-gather reduction           | `P_before / P_after`                        |
| Client-side index cache            | ~200-500 B per index                        |
| Client-side connection overhead    | ~120-300 B per node                         |
| Scoring formula                    | `rttBucket × (inFlight+1)/cwnd × shardCost` |

---

## Appendix B: Benchmark Data

All data from a live 3-node OpenSearch cluster. 60,000 requests per workload at 320 req/s with 32 concurrent workers.

### End-to-End Results

```
Requests:   60,026 total, 0 failures (100% success)
Duration:   3m 08s
Throughput: 320 req/s
Latency:    p50=9ms  p95=20ms  p99=24ms  max=43ms
```

### Per-Index Routing Distribution

Three indexes running simultaneously, three completely different distributions:

| Index                     | Shards       | Node Distribution | Behavior                    |
| ------------------------- | ------------ | ----------------- | --------------------------- |
| demo-replicated (1P/2R)   | All 3 nodes  | 35% / 35% / 30%   | Balanced, cwnd-driven       |
| demo-partial (1P/1R)      | 2 of 3 nodes | 50% / 50% / 0%    | Non-shard node excluded     |
| demo-primary-only (1P/0R) | 1 node       | 0% / 0% / 100%    | All traffic to shard holder |

### Operation-Aware Routing on demo-replicated

| Workload   | Primary Node | Replica Nodes | Dominant Factor              |
| ---------- | ------------ | ------------- | ---------------------------- |
| search     | 13%          | 42% / 45%     | Replica preferred (cost 1.0) |
| write-only | 30%          | 34% / 36%     | Primary preferred (cost 1.0) |
| read-write | 29%          | 36% / 35%     | Costs cancel → cwnd-driven   |

### Routing Overhead

| Operation       | Latency | Allocations |
| --------------- | ------- | ----------- |
| Search routing  | ~259 ns | 0 allocs/op |
| Bulk routing    | ~231 ns | 0 allocs/op |
| Doc get routing | ~452 ns | 0 allocs/op |
| Cluster health  | ~187 ns | 0 allocs/op |

Reference: `opensearchtransport/routing_benchmark_test.go` (Apple M4 Pro, arm64, 2026-03-04).

---

_Generated from opensearch-go v4 source and benchmark data._

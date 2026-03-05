# Connection Scoring and Request Routing

The default router scores every connection on each request using a single formula -- `rtt * (inFlight+1)/cwnd * shardCost` -- and picks the lowest. Because the inputs (RTT buckets, in-flight counts, AIMD congestion windows) change between requests, this is dynamic per-request scoring rather than static path computation. Affinity is the emergent result: rendezvous hashing gives key-to-node consistency, and scoring gives quality. The combination improves OS page-cache hit rates, eliminates coordinator hops, and naturally distributes aggregate traffic across the fleet.

## Quick Start

```go
import "github.com/opensearch-project/opensearch-go/v4/opensearchtransport"

router := opensearchtransport.NewDefaultRouter()

client, err := opensearch.NewClient(opensearch.Config{
    Addresses:             []string{"https://node1:9200", "https://node2:9200"},
    DiscoverNodesOnStart:  true,
    DiscoverNodesInterval: 30 * time.Second,
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

## Available Routers

The transport provides three routers at increasing levels of sophistication:

| Router      | Constructor             | Routing Strategy                                                                    |
| ----------- | ----------------------- | ----------------------------------------------------------------------------------- |
| Round-Robin | `NewRoundRobinRouter()` | Coordinating-only nodes if available, otherwise round-robin across all nodes        |
| Mux         | `NewMuxRouter()`        | Role-based routing by operation type (search -> data, bulk -> ingest, etc.)         |
| Default     | `NewDefaultRouter()`    | Role-based + per-index scoring with RTT, congestion, and operation-aware shard cost |

`NewDefaultRouter` is the recommended default for production deployments. The other two routers are useful for simpler topologies or for isolating routing behavior during debugging.

## How It Works

### Routing Chain

Each request flows through a policy chain:

```
Perform(req)
  -> IfEnabledPolicy evaluates:
     if coordinating-only nodes exist (RolePolicy(RoleCoordinatingOnly)):
       -> poolRouter(coordinatingOnly) -> coordinating-only nodes with scoring
     else:
       -> PolicyChain:
          1. MuxPolicy(scoredRoutes) -> role-based + connection scoring
          2. RoundRobinPolicy            -> fallback for unmatched requests
```

The MuxPolicy matches requests by HTTP method and path (for example, `POST /{index}/_search` routes to search or data nodes). Each role-based sub-policy is wrapped with a `poolRouter` that intercepts the pool returned by the role policy and applies connection scoring to its connections.

For requests without an index in the path (system endpoints such as `/_cluster/health`), the router scores all active connections by `rtt * (inFlight+1)/cwnd` with `shardCost = costUnknown` (a constant, so selection is driven by RTT and congestion only).

### Role-Based Route Table

The MuxPolicy maps OpenSearch REST endpoints to node roles. Each route also carries a server-side thread pool name for per-pool congestion tracking:

| Operation              | Example Path                          | Primary Role  | Fallback | Thread Pool   |
| ---------------------- | ------------------------------------- | ------------- | -------- | ------------- |
| Bulk / Streaming Bulk  | `POST /{index}/_bulk`                 | Ingest        | (none)   | `write`       |
| Reindex                | `POST /_reindex`                      | Ingest        | (none)   | `write`       |
| Ingest Pipelines       | `PUT /_ingest/pipeline/{id}`          | Ingest        | (none)   | `management`  |
| Search / Count         | `POST /{index}/_search`               | Search (3.0+) | Data     | `search`      |
| Multi-search           | `POST /_msearch`                      | Search (3.0+) | Data     | `search`      |
| Delete/Update by Query | `POST /{index}/_delete_by_query`      | Search (3.0+) | Data     | `search`      |
| Scroll / Clear Scroll  | `POST /_search/scroll`                | Search (3.0+) | Data     | `search`      |
| Point-in-Time          | `POST /{index}/_search/point_in_time` | Search (3.0+) | Data     | `search`      |
| Search Shards          | `GET /{index}/_search_shards`         | Search (3.0+) | Data     | `search`      |
| Validate Query         | `POST /{index}/_validate/query`       | Search (3.0+) | Data     | `search`      |
| Search Templates       | `POST /{index}/_search/template`      | Search (3.0+) | Data     | `search`      |
| Multi-Search Templates | `POST /{index}/_msearch/template`     | Search (3.0+) | Data     | `search`      |
| Rank Evaluation        | `POST /{index}/_rank_eval`            | Search (3.0+) | Data     | `search`      |
| Document Get / MGet    | `GET /{index}/_doc/{id}`              | Search (3.0+) | Data     | `get`         |
| Explain                | `GET /{index}/_explain/{id}`          | Search (3.0+) | Data     | `get`         |
| Source                 | `GET /{index}/_source/{id}`           | Search (3.0+) | Data     | `get`         |
| Term Vectors           | `POST /{index}/_termvectors/{id}`     | Search (3.0+) | Data     | `get`         |
| Multi-Term Vectors     | `POST /{index}/_mtermvectors`         | Search (3.0+) | Data     | `get`         |
| Field Capabilities     | `POST /{index}/_field_caps`           | Search (3.0+) | Data     | `management`  |
| Doc Index / Create     | `PUT /{index}/_doc/{id}`              | Data          | (none)   | `write`       |
| Doc Update             | `POST /{index}/_update/{id}`          | Data          | (none)   | `write`       |
| Doc Delete             | `DELETE /{index}/_doc/{id}`           | Data          | (none)   | `write`       |
| Searchable Snapshots   | `POST /_snapshot/{repo}/_mount`       | Warm (2.4+)   | Data     | `management`  |
| Index Settings         | `PUT /{index}/_settings`              | Warm (2.4+)   | Data     | `management`  |
| Refresh                | `POST /{index}/_refresh`              | Data          | (none)   | `refresh`     |
| Flush / Synced Flush   | `POST /{index}/_flush`                | Data          | (none)   | `flush`       |
| Force Merge            | `POST /{index}/_forcemerge`           | Data          | (none)   | `force_merge` |
| Cache Clear            | `POST /{index}/_cache/clear`          | Data          | (none)   | `management`  |
| Segments               | `GET /{index}/_segments`              | Data          | (none)   | `management`  |
| Recovery               | `GET /{index}/_recovery`              | Data          | (none)   | `management`  |
| Shard Stores           | `GET /{index}/_shard_stores`          | Data          | (none)   | `management`  |
| Index Stats            | `GET /{index}/_stats`                 | Data          | (none)   | `management`  |
| Rethrottle             | `POST /_reindex/{taskId}/_rethrottle` | Data          | (none)   | `management`  |
| Everything else        | `GET /_cluster/health`                | Round-robin   | --       | (default)     |

When a preferred role has no nodes (for example, no dedicated search nodes in clusters running OpenSearch versions prior to 3.0), the request falls through to the next role in the chain.

### Index Slots -- Per-Index State

Every index that receives traffic gets an **index slot** -- a dedicated state container in the client that tracks everything the routing system needs to make per-index decisions. The index slot is the central organizing concept that ties rendezvous hashing, scoring, and fan-out together.

An index slot is created lazily on the first request for an index and evicted after 90 minutes of inactivity (configurable via `WithIdleEvictionTTL`). All index slots are held in a lock-free cache (`indexSlotCache`) keyed by index name.

#### What an Index Slot Tracks

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

Each field serves a specific consumer in the routing pipeline:

| Field            | Updated By                    | Read By           | Purpose                                                   |
| ---------------- | ----------------------------- | ----------------- | --------------------------------------------------------- |
| `shardNodeNames` | Discovery (`/_cat/shards`)    | `rendezvousTopK`  | Shard-aware hard partition: shard nodes fill slots first  |
| `shardNodeNames` | Discovery (`/_cat/shards`)    | `calcConnScore`   | Shard cost multiplier: primary vs replica counts per node |
| `shardNodes`     | Discovery (`/_cat/shards`)    | `effectiveFanOut` | Floor for fan-out (cover all nodes with data)             |
| `requestDecay`   | Every request (`getOrCreate`) | `effectiveFanOut` | Volume-driven fan-out growth                              |
| `fanOut`         | `effectiveFanOut`             | `poolRouter.Eval` | Number of candidate nodes (K) for rendezvous              |
| `idleSince`      | Discovery cycle               | Discovery cycle   | Eviction of unused index slots                            |

#### How It Fits the Routing Pipeline

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

The index slot is what makes fan-out and shard placement _per-index_. Without it, the system would have a single global fan-out, which would be incorrect: a hot `orders` index needs a wide fan-out (many candidates), while a cold `config` index should use minimal resources. Per-connection state (RTT ring, congestion windows, in-flight counters) is node-specific and shared across all indexes.

#### Per-Node State Within the Slot

The index slot knows which nodes host its shards and how many primary vs replica shards each node holds. This per-node-per-index information feeds two mechanisms:

1. **Shard-aware partitioning** -- `rendezvousTopK` gives priority to nodes listed in the slot's `shardNodeNames`. These nodes have the index's data warm in the OS page cache. Non-shard nodes are relegated to later slots and only used when the fan-out exceeds the shard node count.

2. **Shard cost multiplier** -- When scoring, the slot provides per-node `shardNodeInfo{Primaries, Replicas}`. The cost table is operation-aware: read routes prefer replica-hosting nodes (1.0x) and penalize primary-only nodes (2.0x), while write routes invert the preference. The cost is per-index, not global: the same node might be a replica-only host for `orders` (good for reads) and a primary-only host for `payments` (good for writes).

#### RTT and Congestion: Per-Connection, Not Per-Slot

The RTT bucket and congestion window are measured per-connection, not stored in the index slot. This is intentional: RTT is a property of the network path between the client and a specific node, not a property of the index. Similarly, the congestion window and in-flight count reflect per-node, per-thread-pool capacity and demand. The same node has the same RTT and the same `search` pool congestion state regardless of which index is being queried. The index slot provides the _index-specific_ context (shard placement, fan-out) while each connection provides the _node-specific_ context (RTT, congestion windows, in-flight counts, warmup state).

### Rendezvous Hashing (Node Subset Selection)

Given a key K (the index name, or `{index}/{routingKey}` for document-level requests), the client selects a stable subset of N nodes from the role pool:

```
For each connection in the pool:
  weight = FNV-1a(K + "\x00" + connection.URL)
  (for document-level: K = indexName + "/" + routingKey)
Sort by weight descending, take top N
```

This is [rendezvous (highest random weight) hashing](https://en.wikipedia.org/wiki/Rendezvous_hashing). Two properties are relevant:

1. **Stability** -- Adding or removing a node redistributes only the keys that were assigned to that node. All other assignments remain unchanged.
2. **Uniformity** -- Keys distribute evenly across all nodes.

#### Shard-Aware Hard Partition

When `/_cat/shards` data is available, the candidate list is partitioned before hashing:

```
Phase 1: Fill slots from shard-hosting nodes (data is warm in the OS page cache)
Phase 2: Fill remaining slots from non-shard nodes (these nodes proxy to shard hosts)
```

Within each partition, nodes are traversed tier-by-tier in RTT order (nearest first), with rendezvous hashing providing stable assignment within each tier.

Only shards in `STARTED` state are considered. Shards in `INITIALIZING`, `RELOCATING`, or `UNASSIGNED` state are excluded because their nodes cannot yet serve requests for those shards.

#### Tier-by-Tier Slot Filling

The slot-filling algorithm constructs a fan-out set of K nodes from a list pre-sorted by RTT bucket (nearest first). Within each RTT tier, when more nodes exist than remaining slots, rendezvous hashing selects the winners. The result is a _mixed-tier_ candidate set:

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

After filling, the jitter counter rotates the slot array so that different clients present the candidates in different order, breaking fleet-level symmetry.

All K candidates compete on every request. Slot filling determines _which_ nodes enter the consideration pool; scoring determines _which one_ wins each request. Nodes from different RTT tiers coexist in the same fan-out set and are always eligible.

#### Jitter Rotation

A per-client atomic counter rotates the result within the N-element slot set:

```
offset = jitter.Add(1) % N
result = rotate(slots, offset)
```

This breaks fleet-level symmetry: different clients present the same N candidates in a different order, so the lowest-scoring candidate differs across clients even for the same index. Without jitter, all clients in the same availability zone would converge on the same preferred node.

### Connection Scoring (Within the Slot Set)

Within the N-node candidate set, the client picks the single best connection using a multiplicative score:

```
score = rttBucket * (inFlight + 1) / cwnd * shardCostMultiplier
```

Where:

- **rttBucket** -- Power-of-two quantized median RTT (see [RTT Bucketing](#rtt-bucketing)).
- **inFlight** -- Client-tracked in-flight request count for the named thread pool on this connection (atomic counter incremented before `RoundTrip`, decremented after).
- **cwnd** -- Per-pool congestion window, managed by AIMD congestion control (see [Congestion Windows](#congestion-windows-aimd)).
- **shardCostMultiplier** -- Operation-aware shard cost (see [Shard Cost Multiplier](#shard-cost-multiplier-operation-aware)).

Lower score wins. The client selects exactly one connection: the candidate with the lowest score. The fan-out set is a _consideration pool_, not a round-robin target. After each request, the winning node's in-flight count increases, so subsequent requests may select a different winner from the same candidate set. Load distributes through utilization-driven rebalancing, not rotation.

The `(inFlight + 1) / cwnd` term is a utilization ratio. A node with 3 in-flight requests and cwnd=13 (`search` pool on a 4-core node) has utilization `4/13 = 0.31`. A node with 0 in-flight requests has utilization `1/13 = 0.077`. The `+1` prevents zero-utilization ties and provides a natural bias toward idle nodes.

When a thread pool is overloaded (delta(rejected) > 0 or HTTP 429), the score is `math.MaxFloat64` -- effectively removing the node from consideration for that pool until the stats poller clears the overloaded flag.

The score uses the quantized **RTT bucket** rather than raw RTT. Bucketing absorbs jitter within a network tier: two same-AZ nodes at 180 us and 220 us both land in bucket 8 and compete purely on utilization and shard cost. Raw microseconds would introduce spurious differentiation between peers in the same tier.

#### RTT Bucketing

Each connection maintains a ring buffer of health-check RTT samples (default 12 slots, derived from `2 * ceil(discoverNodesInterval / resurrectTimeoutInitial)`). The median sample is bucketed with power-of-two quantization:

```
bucket = max(rttBucketFloor, floor(log2(microseconds)))
```

Where `rttBucketFloor = 8` (corresponding to 256 us). Sub-256 us latencies clamp to the floor, collapsing same-AZ peers into a single bucket. The floor is adjustable (7 = 128 us, 6 = 64 us) but 256 us provides a good default for absorbing measurement noise without losing meaningful tier separation. Typical values:

| Location             | Raw RTT  | Bucket |
| -------------------- | -------- | ------ |
| Same AZ              | ~200us   | 8      |
| Cross-AZ same region | ~1-3ms   | 9-11   |
| Cross-region         | ~20-80ms | 14-16  |

Unknown RTT (no samples yet) is assigned a large sentinel value (`1 << 30`) so that new connections are deprioritized until measured. The median naturally transitions from the unknown sentinel to a real tier once more than half the ring buffer contains measured data -- a built-in warmup gate that takes approximately `(ringSize/2 + 1) * healthCheckInterval` to pass (roughly 35 seconds with defaults).

##### Power-of-Two Latency Tiers

Power-of-two bucketing maps naturally onto network latency tiers. Each network hop roughly doubles round-trip time, and each doubling increments the bucket by exactly 1. The bucket _ratio_ between tiers determines the traffic split at equilibrium:

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

The bucket value provides cold-start and idle-state preference for local nodes (lower RTT produces a lower score when utilization is equal). At sustained load, utilization-driven overflow naturally distributes traffic across all active tiers as local nodes fill their congestion windows. See [Steady-State Traffic Distribution](#steady-state-traffic-distribution) for details.

When only local nodes are active (single-tier operation), the RTT bucket differentiates among local peers via utilization and shard cost alone. The bucket's role reduces to a tiebreaker when all nodes have similar utilization.

#### Congestion Windows (AIMD)

Each connection maintains a per-thread-pool congestion window (`cwnd`) that tracks the node's estimated capacity for that pool. The congestion control uses TCP-style AIMD (Additive Increase, Multiplicative Decrease) with slow start:

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

**Thread pool discovery**: `/_nodes/_local/http,os,thread_pool` returns each pool's configured size, which sets `maxCwnd`. For example, on a 4-core node, the `search` pool typically has `maxCwnd = 13` (formula: `int(1.5 * cores + 1)`).

**Stats polling**: `/_nodes/_local/stats/jvm,breaker,thread_pool` returns runtime stats per pool. The stats poller calls `applyPoolAIMD` for each pool on each node:

```
    AIMD state machine:

    delta(rejected) > 0?
         ├── YES -> set overloaded, cwnd /= 2, ssthresh = cwnd
         │          (pool is saturated; hard overload signal)
         │
         └── NO  -> clear overloaded
                    │
                    delta(completed) > 0?
                    ├── NO  -> no change (idle pool)
                    │
                    └── YES -> determine congestion signal:
                               │
                               ├── RESIZABLE pool (has wait_time):
                               │   wait_per_completed >= 1ms? -> congested
                               │
                               └── Other pools (fallback):
                                   queue > 0 AND active >= maxCwnd? -> congested

                    congested?
                    ├── YES -> cwnd /= 2 (multiplicative decrease)
                    │
                    └── NO  -> cwnd < ssthresh?
                               ├── YES -> cwnd *= 2 (slow start: double)
                               └── NO  -> cwnd += 1 (congestion avoidance)

    All cwnd updates are clamped to [1, maxCwnd].
```

**Pre-quorum behavior**: Before pool info is available (before the first `/_nodes/_local/http,os,thread_pool` response), the client uses a synthetic cwnd of `4 * defaultServerCoreCount` (= 32). Once node discovery provides per-node `allocatedProcessors`, the default pool is resized to `4 * allocatedProcessors` for that connection. This provides reasonable capacity-aware scoring before real thread pool data arrives.

**Convergence from cold start**: Under sustained load, cwnd reaches the thread pool ceiling in approximately 4 poll cycles (~20s at the default 5s interval). For a `search` pool with `maxCwnd = 13`: cwnd progresses 1 -> 2 -> 4 -> 8 -> 13. Each connection converges independently without coordination between clients or nodes.

**In-flight tracking**: Each `Perform` call brackets the HTTP round-trip with `addInFlight(poolName)` / `releaseInFlight(poolName)`. The `NextHop.PoolName` field carries the thread pool name from routing to execution:

```
    Perform(req):
      hop = router.Route(req)        // returns NextHop{Conn, PoolName}
      conn.addInFlight(hop.PoolName)  // atomic increment
      res = transport.RoundTrip(req)
      conn.releaseInFlight(hop.PoolName)  // atomic decrement
```

**HTTP 429 handling**: When a response returns 429 (Too Many Requests), the handler uses `TryLock` on the pool's mutex to set the overloaded flag and halve cwnd. The request is retried on a different node. The overloaded flag is only cleared by the stats poller when `delta(rejected) == 0`, preventing premature resumption.

##### Pool Registry

Each connection holds a `poolRegistry` -- a `sync.Map` of pool name to `*poolCongestion` plus a synthetic default pool:

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

Requests with an empty `PoolName` (non-scored routes) use the default pool. Requests to unknown pool names also fall back to the default pool. Pools are created on discovery and removed when the node stops reporting them.

##### Per-Index Fan-Out Counter (`decayCounter`)

Each index slot has a simple decay counter used _only_ for dynamic fan-out sizing, not for scoring:

```
counter = counter * 0.999 + 1.0
```

This counter increments by exactly 1.0 per request, with per-request multiplicative decay. At constant rate it converges to `1/(1-0.999) = 1000`. The fan-out calculation divides this counter by `fanOutPerRequest` (default 500) to determine how many extra nodes to include: `rateFanOut = int(counter / 500) + 1`.

This counter is not involved in scoring or node selection; it controls only the size of the candidate set.

#### Score Components Visualization

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

The score is purely ordinal -- the client picks the lowest. What matters is relative ranking, not absolute magnitude.

#### Shard Cost Multiplier (Operation-Aware)

When `/_cat/shards` data is available, the client knows whether each node hosts primary shards, replica shards, or both for the target index. The score includes a `shardCostMultiplier` that biases selection based on both the node's shard composition and the operation type (read vs write).

Each pool router is constructed with a cost table selected at policy construction time:

**Read routes** (search, document get, snapshots) -- `shardCostForReads`:

| Shard Role                | Cost | Notes                                                  |
| ------------------------- | ---- | ------------------------------------------------------ |
| Replica-only              | 1.0  | Preferred: lock-free Lucene snapshot reads             |
| Mixed (primary + replica) | 1.0  | Can serve reads locally; utilization differentiates    |
| Primary-only              | 2.0  | Primaries contend with writes                          |
| Unknown                   | 32.0 | No shard data; heavily penalized to prefer known nodes |

**Write routes** (bulk, index, update, delete) -- `shardCostForWrites`:

| Shard Role                | Cost | Notes                                                  |
| ------------------------- | ---- | ------------------------------------------------------ |
| Primary-only              | 1.0  | Preferred: writes go to primary shard first            |
| Mixed (primary + replica) | 1.0  | Can serve writes locally; utilization differentiates   |
| Replica-only              | 2.0  | Replica must proxy to primary -- coordinator hop       |
| Unknown                   | 32.0 | No shard data; heavily penalized to prefer known nodes |

Mixed nodes (hosting both primaries and replicas for the target index) get `min(replica, primary)` cost -- 1.0 for both tables -- since they can serve both reads and writes locally. Load-based differentiation between mixed nodes is handled by the utilization ratio `(inFlight + 1) / cwnd`, not by this multiplier.

When `/_cat/shards` data is unavailable, all nodes receive the unknown penalty (32x). Since this applies uniformly, it does not affect relative ordering: scoring degrades to RTT \* utilization only, and the penalty cancels out in comparison.

**Workload-dependent distribution.** The operation-aware cost tables produce measurably different node distributions per workload type. On a 3-node cluster where all nodes hold shard copies (1 primary, 2 replicas), measured at 320 req/s with 32 concurrent workers:

- **Search (read-only)**: replica-hosting nodes received 42-45% each, primary received 13% (replica sc=1.0 preferred over primary sc=2.0)
- **Write-only**: primary-hosting node received more traffic (primary sc=1.0 preferred over replica sc=2.0)
- **Read-write (50/50)**: all nodes received 29-36% (alternating cost tables average to ~1.5, making cwnd the dominant differentiator)

This behavior is automatic. No workload-specific configuration is required.

### AZ-Aware Overflow

The multiplicative score formula creates natural tier-based overflow. Because all candidates in the fan-out set are scored on every request, traffic splits across RTT tiers based on available capacity. This is **not** a "fill the local tier to saturation, then overflow" model.

#### How Fast Does Crossover Happen?

With congestion-window scoring, crossover between a local node (bucket=8) and a remote node (bucket=B) is immediate when the local node has higher utilization:

```
local_score  = 8 * (inFlight_local + 1) / cwnd
remote_score = B * (0 + 1) / cwnd           (idle remote)
crossover when: 8 * (inFlight + 1) / cwnd > B * 1 / cwnd
                inFlight > B/8 - 1
```

For a remote node in bucket 11: `inFlight > 11/8 - 1 = 0.375`, so any in-flight request on the local node is sufficient. For bucket 14: `inFlight > 14/8 - 1 = 0.75`, so 1 in-flight request triggers crossover. With per-pool congestion windows (e.g., `search` cwnd=13), multiple concurrent requests distribute across tiers immediately.

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

#### Steady-State Traffic Distribution

At sustained load across multiple RTT tiers, the congestion-window scoring produces proportional traffic distribution based on available capacity. The `(inFlight + 1) / cwnd` utilization ratio naturally equalizes across nodes:

- Nodes with the same `cwnd` (homogeneous cluster) reach equal in-flight counts at equilibrium.
- The RTT bucket acts as a tiebreaker at equal utilization, preferring local nodes.
- When in-flight counts grow high enough to overcome the bucket ratio, remote nodes absorb overflow.

The distribution is not perfectly equal across tiers. Instead, local nodes carry slightly more traffic proportional to their bucket advantage, which is the desired behavior: use local capacity first, overflow proportionally to remote capacity as local nodes fill.

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

#### Cold-Start and Idle Behavior

When traffic is low or absent, all nodes have `inFlight = 0` and the utilization ratio is `1/cwnd` uniformly. The `rttBucket` multiplier alone determines ranking: local nodes are preferred. As load increases and in-flight counts rise, the utilization ratio dominates and traffic overflows to remote tiers.

This two-phase behavior is intentional:

1. **Cold start / light load**: RTT-based preference keeps traffic local, benefiting from warm TCP connections and filesystem page cache.
2. **Sustained load**: Utilization-driven distribution prevents local-node saturation. Traffic naturally overflows to remote tiers as local nodes fill their congestion windows.

### Dynamic Fan-Out

The number of nodes in each index's slot set (N) adapts to workload:

```
effectiveFanOut = max(minFanOut, shardNodeCount, rateFanOut)
effectiveFanOut = min(effectiveFanOut, maxFanOut)
effectiveFanOut = min(effectiveFanOut, activeNodeCount)
```

Where:

- **minFanOut** -- Configuration floor (default: 1).
- **shardNodeCount** -- Number of nodes hosting `STARTED` shards for this index (from `/_cat/shards`). Ensures the candidate set covers all nodes with relevant data.
- **rateFanOut** -- Derived from the request decay counter: `int(counter / fanOutPerReq) + 1`. High request volume drives the counter up, which increases rateFanOut, which expands the slot set.
- **maxFanOut** -- Configuration cap (default: 32). Prevents pathologically sharded indexes from inflating the candidate set to the entire cluster.

When traffic drops, the counter decays and fan-out contracts. Per-index overrides via `WithIndexFanOut` bypass the dynamic calculation entirely.

#### Fan-Out Bounds

The effective fan-out is clamped between a hard floor and a hard ceiling. Within those bounds, three inputs compete via `max()` to determine the actual value:

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

At low request rates, fan-out rests at the shard node count: the candidate set covers exactly the nodes with data. As traffic increases, the decay counter grows and `rateFanOut` pushes fan-out above the shard floor, spreading coordinator load across additional nodes. The `maxFanOut` ceiling prevents pathologically sharded indexes (shards on every node) from expanding the candidate set to the entire cluster:

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

### Idle Eviction

Index slots that receive no requests are evicted from the cache after `IdleEvictionTTL` (default: 90 minutes). On each discovery cycle:

1. The slot's decay counter receives one decay step.
2. If the counter drops below 1.0, the slot is marked idle with a timestamp.
3. If the slot has been idle longer than the TTL, it is deleted.
4. If a new request arrives at any point, the idle marker is cleared.

This prevents unbounded cache growth in clusters serving many transient indexes.

## Shard Placement Discovery

During each discovery cycle, the client fetches:

```
GET /_cat/shards?format=json&h=index,shard,prirep,state,node
```

This provides:

- Which nodes host shards for each index
- Whether each shard is a primary (`p`) or replica (`r`)
- The shard's state (`STARTED`, `INITIALIZING`, `RELOCATING`, `UNASSIGNED`)

Only `STARTED` shards are included in the placement map. The data feeds three routing mechanisms:

1. **Hard partition** in rendezvousTopK -- shard-hosting nodes fill slots before non-shard nodes.
2. **Fan-out floor** -- the distinct node count per index acts as a floor for fan-out.
3. **Shard cost multiplier** -- per-node primary/replica counts drive the operation-aware shard cost in scoring.

If `/_cat/shards` fails (for example, due to a missing `indices:monitor/stats` permission), the client continues without shard data:

- Fan-out stays at minFanOut (or rateFanOut if request-driven growth applies)
- No shard-aware hard partition: all nodes are treated equally by rendezvousTopK
- No shard cost data: all nodes receive the unknown cost (32x), which cancels out since it applies uniformly

The failure is logged at debug level and re-attempted on the next discovery cycle.

### Failure-Triggered Shard Map Refresh

Between discovery cycles, the shard placement map can become stale. A node may flap (fail, have its shards relocated, then recover), or a shard may be reinitializing or relocating. When this occurs, the client routes requests to a node that no longer hosts the target shards, forcing the server to act as a coordinator proxy -- exactly the overhead that connection scoring is designed to eliminate.

The client detects this situation reactively: when a request to a node fails (transport error or retryable HTTP status), the failing connection is marked with a `needsCatUpdate` flag. This flag has three effects:

1. **Exclusion from scored routing** -- `rendezvousTopK` filters out connections with `needsCatUpdate`. The node is excluded from all index candidate sets until the flag is cleared. Round-robin fallback and zombie tryout routing still include the node, so it remains reachable for non-scored traffic.

2. **Survives resurrection** -- Unlike the dead/alive lifecycle, `needsCatUpdate` is an independent metadata bit that persists through resurrection. A node can pass health checks, return to the active pool, and serve round-robin requests, but it remains excluded from scored candidate sets until shard placement is reconfirmed. This is the essential design point: the shard map for a resurrected node is not trusted until ground truth from `/_cat/shards` is obtained.

3. **Expedited shard refresh** -- Setting the flag triggers `scheduleCatRefresh()`, which schedules a lightweight `/_cat/shards`-only refresh (no full node discovery). The urgency scales with the fraction of affected connections:

   ```
   fraction  = flaggedConnections / totalActiveConnections
   interval  = discoverNodesInterval * (1 - fraction)
               clamped to [5s, discoverNodesInterval]
   ```

   | Scenario               | Fraction | Effective interval (30s base) |
   | ---------------------- | -------- | ----------------------------- |
   | 1 of 32 nodes flagged  | 3%       | ~29s (barely expedited)       |
   | 8 of 32 nodes flagged  | 25%      | ~22s                          |
   | 16 of 32 nodes flagged | 50%      | ~15s                          |
   | 32 of 32 nodes flagged | 100%     | 5s (floor)                    |

   This prevents thundering-herd `/_cat/shards` calls for single-node failures while responding urgently to large-scale topology changes.

When the `/_cat/shards` fetch completes successfully, the `needsCatUpdate` flag is cleared on all connections and any pending expedited refresh timer is cancelled. Connections re-enter scored candidate sets with fresh shard placement data.

**Design note**: a server can fail and be resurrected without the client observing the event, and this is intentional. The `needsCatUpdate` flag is set only when the client _observes_ a failure on a connection it is actively using. If a node flaps but the client has no in-flight requests to that node, the topology change is discovered on the next regular discovery cycle. The client reacts proportionally to the failures it experiences, not to cluster events it cannot observe.

#### Observer Hook

Implement `OnShardMapInvalidation(event ShardMapInvalidationEvent)` on your `ConnectionObserver` to monitor when connections are flagged. The event includes the connection URL, name, reason (`"transport_error"` or `"http_status_retry"`), and timestamp.

## Configuration

### Functional Options

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

### Client Config Fields

```go
client, err := opensearch.NewClient(opensearch.Config{
    // ...
    Router: router,
})
```

### Option Reference

| Option                     | Default | Description                                                              |
| -------------------------- | ------- | ------------------------------------------------------------------------ |
| `WithMinFanOut(n)`         | 1       | Minimum nodes in an index slot. Floor for fan-out.                       |
| `WithMaxFanOut(n)`         | 32      | Maximum nodes in an index slot. Caps pathologically sharded indexes.     |
| `WithDecayFactor(d)`       | 0.999   | Fan-out counter decay factor. Must be in (0, 1). Higher = longer memory. |
| `WithFanOutPerRequest(f)`  | 500     | Decay counter value that maps to +1 fan-out node.                        |
| `WithIdleEvictionTTL(d)`   | 90 min  | How long an idle index slot persists before eviction.                    |
| `WithIndexFanOut(m)`       | nil     | Per-index fan-out overrides. Bypasses dynamic calculation.               |
| `WithShardExactRouting(b)` | true    | Enable/disable murmur3 shard-exact routing. Env var overrides.           |

### Environment Variables

Two environment variables provide runtime control over routing and discovery behavior. Both are evaluated once at client init time and are immutable after. Environment variable settings override programmatic `RouterOption` values.

#### `OPENSEARCH_GO_ROUTING_CONFIG`

Controls request-time routing behavior. Format: comma-separated items where `+`/`-` prefixed items toggle bitfield flags and `key=value` items set parameters.

**Bitfield flags:**

| Flag          | Default | `-` effect                                     |
| ------------- | ------- | ---------------------------------------------- |
| `shard_exact` | enabled | Disable murmur3 shard-exact connection routing |

**Examples:**

```bash
# Disable shard-exact routing
OPENSEARCH_GO_ROUTING_CONFIG=-shard_exact
```

#### `OPENSEARCH_GO_DISCOVERY_CONFIG`

Controls which server calls are made during the discovery cycle.

**Bitfield flags:**

| Flag                 | Default | `-` effect                                                                 |
| -------------------- | ------- | -------------------------------------------------------------------------- |
| `cat_shards`         | enabled | Skip `GET /_cat/shards`. No shard placement data.                          |
| `routing_num_shards` | enabled | Skip `GET /_cluster/state/metadata`. Shard-exact falls back to rendezvous. |
| `cluster_health`     | enabled | Skip `GET /_cluster/health?local=true` probes.                             |
| `node_stats`         | enabled | Skip `GET /_nodes/_local/stats` polling.                                   |

**Examples:**

```bash
# Skip metadata fetch and node stats (reduces server calls)
OPENSEARCH_GO_DISCOVERY_CONFIG=-routing_num_shards,-node_stats

# Minimal discovery: only node membership, no enrichment
OPENSEARCH_GO_DISCOVERY_CONFIG=-cat_shards,-routing_num_shards,-cluster_health,-node_stats
```

## Architecture Diagram

```
                    ┌───────────────────────────────────────────────────────┐
                    │                    Client.Perform(req)                │
                    │                                                       │
                    │  1. router.Route(ctx, req)                            │
                    └─────────────────────┬─────────────────────────────────┘
                                          │
                    ┌─────────────────────▼─────────────────────────────────┐
                    │              IfEnabledPolicy                          │
                    │                                                       │
                    │  ┌──────────────────────────────────────────────┐     │
                    │  │ RolePolicy(CoordinatingOnly) + poolRouter    │     │
                    │  │ (if coordinating nodes exist, routes here)   │     │
                    │  └──────────────────┬───────────────────────────┘     │
                    │                     │ else (no coordinating nodes)    │
                    │  ┌──────────────────▼───────────────────────────┐     │
                    │  │ PolicyChain                                  │     │
                    │  │                                              │     │
                    │  │  MuxPolicy (HTTP pattern matching)           │     │
                    │  │                                              │     │
                    │  │  POST /{index}/_search -> poolRouter(        │     │
                    │  │    search -> data -> null)                   │     │
                    │  │  POST /_bulk -> poolRouter(ingest)           │     │
                    │  │  ...                                         │     │
                    │  └──────────────────┬───────────────────────────┘     │
                    │                     │ fall through                    │
                    │  ┌──────────────────▼───────────────────────────┐     │
                    │  │ RoundRobinPolicy (fallback)                  │     │
                    │  └──────────────────────────────────────────────┘     │
                    └───────────────────────────────────────────────────────┘

                    ┌───────────────────────────────────────────────────────┐
                    │           poolRouter.Eval(req)                        │
                    │                                                       │
                    │  1. inner.Eval(req) -> NextHop{Conn}                  │
                    │  2. Read pre-sorted connections (RLock)               │
                    │  3. Extract index (+ docID) from request path         │
                    │  4. Look up index slot -> fan-out, shard nodes        │
                    │  5. rendezvousTopK(key, conns, fanOut, shardNodes)    │
                    │     a. Partition: shard-hosting vs non-shard nodes    │
                    │     b. Fill K slots tier-by-tier (nearest RTT first)  │
                    │     c. Rendezvous hash within each tier               │
                    │     d. Jitter-rotate the K-slot result                │
                    │  6. Score: RTT * (inFlight+1)/cwnd * shardCost        │
                    │  7. connScoreSelect: warmup-aware skip/accept         │
                    │  8. Return NextHop{Conn: best, PoolName: poolName}    │
                    └───────────────────────────────────────────────────────┘
```

## Read-After-Write Visibility

The router directs writes toward primary-hosting nodes and reads toward replica-hosting nodes. Because the write and read paths select different nodes, it is worth clarifying the visibility guarantees that OpenSearch provides.

### Replication Model

OpenSearch replicates writes synchronously to all in-sync shard copies before acknowledging the request:

1. The coordinating node forwards the document to the primary shard.
2. The primary writes the document to its translog.
3. The primary replicates the write to all in-sync replicas in parallel.
4. The primary waits for all in-sync replicas to acknowledge.
5. The coordinating node returns the response to the client.

When the client receives a successful response, all in-sync replicas already hold the document in their translogs. Routing a subsequent read to a replica does not introduce a consistency gap.

### Visibility by Operation Type

| Read Operation                              | Visible Immediately? | Explanation                                                                                                                                                |
| ------------------------------------------- | -------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET /{index}/_doc/{id}`                    | Yes                  | Realtime GET reads from the translog, bypassing Lucene segments. All in-sync replicas hold the document before the write response is returned.             |
| `POST /{index}/_search`                     | After next refresh   | Search reads from Lucene segments. The refresh interval (default 1 s) governs visibility and applies uniformly to all shard copies, including the primary. |
| Write with `?refresh=wait_for`, then search | Yes                  | The write blocks until the next refresh completes on all shard copies before returning.                                                                    |

### Refresh Interval and Search Visibility

The delay before a newly written document appears in search results is determined by the server-side **refresh interval**, not by replication lag. Because the refresh boundary applies to all shard copies at the same time, routing a search back to the node that performed the write would not improve visibility.

Applications that require immediate search visibility after a write should use the `refresh` query parameter on the write request:

- `refresh=wait_for` -- block until the next scheduled refresh completes. Preferred for most use cases.
- `refresh=true` -- force an immediate refresh. Higher cost; avoid in bulk write pipelines.

## Key Properties

| Property                     | Mechanism                                                                                                                                   |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| Cache locality               | Rendezvous hash routes same index to same nodes                                                                                             |
| AZ preference                | RTT bucket scoring: local nodes score lower                                                                                                 |
| Shard cost (operation-aware) | Shard cost multiplier: reads prefer replicas, writes prefer primaries                                                                       |
| Capacity-aware routing       | Per-pool AIMD congestion windows: `(inFlight + 1) / cwnd` utilization ratio                                                                 |
| Overload protection          | Overloaded pools scored at `MaxFloat64`; cleared by stats poller when `delta(rejected) == 0`                                                |
| Fleet symmetry breaking      | Per-client jitter rotation within slot set                                                                                                  |
| Hot index scaling            | Dynamic fan-out from shard count + request rate                                                                                             |
| Cold index eviction          | idleSince + 90min TTL prevents cache bloat                                                                                                  |
| Graceful degradation         | Non-index requests and unhealthy pools fall through to round-robin                                                                          |
| Stale shard recovery         | `needsCatUpdate` excludes failed connections from scoring until `/_cat/shards` confirms                                                     |
| Lock-free fast path          | RTT ring reads, atomic cwnd/inFlight reads, atomic fan-out reads -- no mutexes on the request path except a brief RLock to copy connections |

## Permissions

Without the OpenSearch Security plugin, all endpoints are permitted and no configuration is required.

When the Security plugin is enabled, shard placement discovery requires permissions for `GET /_cat/shards`. The required permissions vary by OpenSearch version:

**OpenSearch 2.17+** (including all 3.x releases): The `/_cat/shards` REST handler dispatches a single transport action `cluster:monitor/shards` (introduced in [#13966](https://github.com/opensearch-project/OpenSearch/pull/13966), backported to 2.17.0). The internal sub-calls to `cluster:monitor/state` and `indices:monitor/stats` execute via `NodeClient` on the same node and are not re-checked by the security filter. Only the top-level permission is needed:

- `cluster:monitor/shards`

**OpenSearch < 2.17**: The `/_cat/shards` REST handler directly calls two transport actions, both checked by the security filter:

- `cluster:monitor/state` (cluster-level)
- `indices:monitor/stats` (index-level, on the requested indices)

### Graceful Degradation

If the client's credentials lack these permissions, the `/_cat/shards` call returns 403 and the client continues without shard data:

- Fan-out stays at minFanOut (or rateFanOut if request-driven growth applies)
- No shard-aware hard partition: all nodes are treated equally by rendezvousTopK
- No shard cost data: all nodes receive the unknown cost (32x), which cancels out since it applies uniformly

This is not a fatal error. The client logs the failure at debug level and re-attempts on the next discovery cycle.

### Built-In Roles

Analysis of the security plugin's static role definitions ([`static_roles.yml`](https://github.com/opensearch-project/security/blob/main/src/main/resources/static_config/static_roles.yml)):

| Role                  | Covers 2.17+? | Covers < 2.17? | Why                                                                                                                                                                                                         |
| --------------------- | ------------- | -------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `all_access`          | Yes           | Yes            | `cluster_permissions: ["*"]` grants everything.                                                                                                                                                             |
| `readall_and_monitor` | Yes           | No             | Has `cluster_monitor` (`cluster:monitor/*`) which covers `cluster:monitor/shards`. Lacks `indices_monitor` on `*`, so `indices:monitor/stats` fails on versions prior to 2.17 where it is checked directly. |
| `kibana_server`       | Yes           | No             | Same as `readall_and_monitor`: has `cluster_monitor` but no `indices_monitor` on arbitrary index patterns.                                                                                                  |
| `readall`             | No            | No             | Only has `cluster_composite_ops_ro` (mget, msearch, scroll). No cluster monitoring permissions.                                                                                                             |

### Custom Role for Shard Discovery

For service accounts that need shard placement data, create a custom role. The permissions needed depend on your minimum supported OpenSearch version.

**OpenSearch 2.17+ only:**

```yaml
connection_routing:
  cluster_permissions:
    - "cluster:monitor/shards"
```

**OpenSearch < 2.17 (or mixed-version deployments):**

```yaml
connection_routing:
  cluster_permissions:
    - "cluster:monitor/shards" # 2.17+ top-level action
    - "cluster:monitor/state" # < 2.17 direct call
  index_permissions:
    - index_patterns:
        - "*"
      allowed_actions:
        - "indices:monitor/stats" # < 2.17 direct call
```

Alternatively, use the built-in action groups for a more concise definition:

```yaml
connection_routing:
  cluster_permissions:
    - "cluster_monitor" # cluster:monitor/* (covers shards + state)
  index_permissions:
    - index_patterns:
        - "*"
      allowed_actions:
        - "indices_monitor" # indices:monitor/* (covers stats)
```

Note: `cluster_monitor` is the broadest option and also covers `cluster:monitor/health` (used by [cluster health probes](cluster_health_checking.md)). If health probes are also in use, a single `cluster_monitor` permission covers both features.

Apply the role via the Security REST API:

```json
PUT /_plugins/_security/api/roles/connection_routing
{
  "cluster_permissions": ["cluster_monitor"],
  "index_permissions": [{
    "index_patterns": ["*"],
    "allowed_actions": ["indices_monitor"]
  }]
}
```

## Comparison with Other Routers

| Feature              | Round-Robin | Mux Router           | Default Router (Mux + Scoring)                                          |
| -------------------- | ----------- | -------------------- | ----------------------------------------------------------------------- |
| Node selection       | Round-robin | Role-based           | Yes, role-based + index-consistent via rendezvous hash                  |
| Cache locality       | None        | None                 | Yes, per-index node affinity keeps OS page cache warm                   |
| AZ awareness         | None        | None                 | Yes, RTT-based scoring with utilization-driven overflow to remote tiers |
| Shard cost           | None        | None                 | Yes, operation-aware: reads prefer replicas, writes prefer primaries    |
| Load balancing       | Uniform     | Uniform within role  | Yes, per-pool AIMD congestion windows with in-flight tracking           |
| Shard awareness      | None        | None                 | Yes, shard-hosting nodes fill candidate slots first via hard partition  |
| Fleet distribution   | None        | None                 | Yes, per-client jitter rotation breaks fleet-level symmetry             |
| Configuration        | None        | None                 | Optional, good defaults out of the box                                  |
| Per-request overhead | O(1)        | O(1) per route match | O(K) per matched route (K = fan-out, typically 1-8)                     |

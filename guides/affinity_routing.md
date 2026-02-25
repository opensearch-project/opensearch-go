# Affinity Routing

Affinity routing directs requests for the same index or document to a stable, bounded subset of nodes. This improves operating-system page-cache hit rates, reduces cross-node coordinator hops, and assigns each client instance a distinct slice of the cluster so that aggregate traffic distributes naturally across the fleet.

## Quick Start

```go
import "github.com/opensearch-project/opensearch-go/v4/opensearchtransport"

router := opensearchtransport.NewSmartRouter()

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
- Self-stabilizing load distribution via time-weighted CPU-cost counters
- Shard-aware node partitioning derived from `/_cat/shards` data
- Automatic `?preference=_local` injection on outgoing requests

## Available Routers

The transport provides three routers at increasing levels of sophistication:

| Router           | Constructor             | Routing Strategy                                                                |
| ---------------- | ----------------------- | ------------------------------------------------------------------------------- |
| Round-Robin      | `NewRoundRobinRouter()` | Coordinating-only nodes if available, otherwise round-robin across all nodes    |
| Mux              | `NewMuxRouter()`        | Role-based routing by operation type (search -> data, bulk -> ingest, etc.)     |
| Smart (Affinity) | `NewSmartRouter()`      | Role-based + per-index affinity with RTT scoring and operation-aware shard cost |

`NewSmartRouter` is the recommended default for production deployments. The other two routers are useful for simpler topologies or for isolating routing behavior during debugging.

## How It Works

### Routing Chain

Each request flows through a policy chain:

```
Perform(req)
  -> injectPreference(req)           // add ?preference=_local (if any Router is set)
  -> PolicyChain evaluates:
     1. CoordinatingPolicy           -> coordinating-only nodes (if any exist)
     2. MuxPolicy(affinityRoutes)    -> role-based + affinity selection
     3. RoundRobinPolicy             -> fallback for unmatched requests
```

The MuxPolicy matches requests by HTTP method and path (for example, `POST /{index}/_search` routes to search or data nodes). Each role-based sub-policy is wrapped with an `affinityPolicyWrapper` that intercepts the pool returned by the role policy and applies affinity selection to its connections.

For requests without an index in the path (system endpoints such as `/_cluster/health`), the affinity wrapper passes through transparently and the request is handled by role-based or round-robin routing.

### Role-Based Route Table

The MuxPolicy maps OpenSearch REST endpoints to node roles:

| Operation                | Example Path                          | Primary Role  | Fallback |
| ------------------------ | ------------------------------------- | ------------- | -------- |
| Bulk / Streaming Bulk    | `POST /{index}/_bulk`                 | Ingest        | (none)   |
| Reindex                  | `POST /_reindex`                      | Ingest        | (none)   |
| Ingest Pipelines         | `PUT /_ingest/pipeline/{id}`          | Ingest        | (none)   |
| Search / Count / Explain | `POST /{index}/_search`               | Search (3.0+) | Data     |
| Multi-search             | `POST /_msearch`                      | Search (3.0+) | Data     |
| Document Get / MGet      | `GET /{index}/_doc/{id}`              | Search (3.0+) | Data     |
| Term Vectors             | `POST /{index}/_termvectors/{id}`     | Search (3.0+) | Data     |
| Multi-Term Vectors       | `POST /{index}/_mtermvectors`         | Search (3.0+) | Data     |
| Delete/Update by Query   | `POST /{index}/_delete_by_query`      | Search (3.0+) | Data     |
| Scroll / Clear Scroll    | `POST /_search/scroll`                | Search (3.0+) | Data     |
| Point-in-Time            | `POST /{index}/_search/point_in_time` | Search (3.0+) | Data     |
| Search Shards            | `GET /{index}/_search_shards`         | Search (3.0+) | Data     |
| Field Capabilities       | `POST /{index}/_field_caps`           | Search (3.0+) | Data     |
| Validate Query           | `POST /{index}/_validate/query`       | Search (3.0+) | Data     |
| Search Templates         | `POST /{index}/_search/template`      | Search (3.0+) | Data     |
| Multi-Search Templates   | `POST /{index}/_msearch/template`     | Search (3.0+) | Data     |
| Rank Evaluation          | `POST /{index}/_rank_eval`            | Search (3.0+) | Data     |
| Searchable Snapshots     | `POST /_snapshot/{repo}/_mount`       | Warm (2.4+)   | Data     |
| Index Settings           | `PUT /{index}/_settings`              | Warm (2.4+)   | Data     |
| Refresh                  | `POST /{index}/_refresh`              | Data          | (none)   |
| Flush / Synced Flush     | `POST /{index}/_flush`                | Data          | (none)   |
| Force Merge              | `POST /{index}/_forcemerge`           | Data          | (none)   |
| Cache Clear              | `POST /{index}/_cache/clear`          | Data          | (none)   |
| Segments                 | `GET /{index}/_segments`              | Data          | (none)   |
| Recovery                 | `GET /{index}/_recovery`              | Data          | (none)   |
| Shard Stores             | `GET /{index}/_shard_stores`          | Data          | (none)   |
| Index Stats              | `GET /{index}/_stats`                 | Data          | (none)   |
| Rethrottle               | `POST /_reindex/{taskId}/_rethrottle` | Data          | (none)   |
| Everything else          | `GET /_cluster/health`                | Round-robin   | --       |

When a preferred role has no nodes (for example, no dedicated search nodes in clusters running OpenSearch versions prior to 3.0), the request falls through to the next role in the chain.

### Index Slots -- Per-Index State

Every index that receives traffic gets an **index slot** -- a dedicated state container in the client that tracks everything the routing system needs to make per-index decisions. The index slot is the central organizing concept that ties rendezvous hashing, scoring, fan-out, and tier-span equalization together.

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
    │  smoothedMaxBucket: 8.0 ── MIAD-smoothed max RTT across fan-out    │
    │  smoothedMaxBucketNano ─── Timestamp of last MIAD update           │
    │                                                                    │
    │  idleSince: 0 ────────── Eviction clock (0 = active, nonzero =     │
    │                           nanos when counter dropped below 1.0)    │
    └────────────────────────────────────────────────────────────────────┘
```

Each field serves a specific consumer in the routing pipeline:

| Field               | Updated By                    | Read By                    | Purpose                                                   |
| ------------------- | ----------------------------- | -------------------------- | --------------------------------------------------------- |
| `shardNodeNames`    | Discovery (`/_cat/shards`)    | `rendezvousTopK`           | Shard-aware hard partition: shard nodes fill slots first  |
| `shardNodeNames`    | Discovery (`/_cat/shards`)    | `affinityScore`            | Shard cost multiplier: primary vs replica counts per node |
| `shardNodes`        | Discovery (`/_cat/shards`)    | `effectiveFanOut`          | Floor for fan-out (cover all nodes with data)             |
| `requestDecay`      | Every request (`getOrCreate`) | `effectiveFanOut`          | Volume-driven fan-out growth                              |
| `fanOut`            | `effectiveFanOut`             | `IndexAffinityPolicy.Eval` | Number of candidate nodes (K) for rendezvous              |
| `smoothedMaxBucket` | `IndexAffinityPolicy.Eval`    | `recordCPUTime`            | Tier-span equalization inflation factor                   |
| `idleSince`         | Discovery cycle               | Discovery cycle            | Eviction of unused index slots                            |

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
    │                                      │
    │     maxBucket = max RTT tier across  │
    │       all K candidates               │  ◄── highest latency tier
    │     slot.updateSmoothedMaxBucket(    │
    │       maxBucket)                     │  ◄── MIAD smoothing update
    └──────────────┬───────────────────────┘
                   │
                   ▼
    ┌──────────────────────────────────────┐
    │  3. SCORING                          │
    │     for each candidate:              │
    │       info = slot.shardNodeInfo(     │
    │         candidate.Name)              │  ◄── primary/replica counts
    │       score = rttBucket              │
    │             * max(counter, 1.0)      │
    │             * shardCost(info)        │
    │     candidate with lowest score wins │
    └──────────────┬───────────────────────┘
                   │
                   ▼
    ┌───────────────────────────────────────┐
    │  4. ATTRIBUTION (after response)      │
    │     smb = slot.smoothedMaxBucket      │  ◄── read from slot
    │     cost = cpuMicros * (smb / bucket) │  ◄── tier-span inflation
    │     winner.affinityCounter.add(cost)  │
    └───────────────────────────────────────┘
```

The index slot is what makes all of these mechanisms _per-index_. Without it, the system would have a single global fan-out and a single global tier-span state, which would be incorrect: a hot `orders` index needs a wide fan-out (many candidates), while a cold `config` index should use minimal resources. The `smoothedMaxBucket` must be per-index because different indexes have different fan-out sets that may span different RTT tiers.

#### Per-Node State Within the Slot

The index slot knows which nodes host its shards and how many primary vs replica shards each node holds. This per-node-per-index information feeds two mechanisms:

1. **Shard-aware partitioning** -- `rendezvousTopK` gives priority to nodes listed in the slot's `shardNodeNames`. These nodes have the index's data warm in the OS page cache. Non-shard nodes are relegated to later slots and only used when the fan-out exceeds the shard node count.

2. **Shard cost multiplier** -- When scoring, the slot provides per-node `shardNodeInfo{Primaries, Replicas}`. The cost table is operation-aware: read routes prefer replica-hosting nodes (1.0x) and penalize primary-only nodes (2.0x), while write routes invert the preference. The cost is per-index, not global: the same node might be a replica-only host for `orders` (good for reads) and a primary-only host for `payments` (good for writes).

#### RTT: Per-Connection, Not Per-Slot

The RTT bucket is measured per-connection via health-check probes, not stored in the index slot. This is intentional: RTT is a property of the network path between the client and a specific node, not a property of the index. The same node has the same RTT for all indexes. The index slot provides the _index-specific_ context (shard placement, fan-out, tier-span state) while each connection provides the _node-specific_ context (RTT, CPU load, warmup state).

### Rendezvous Hashing (Node Subset Selection)

Given a key K (the index name, or `{index}/{docID}` for document-level requests), the client selects a stable subset of N nodes from the role pool:

```
For each connection in the pool:
  weight = FNV-1a(K + "\x00" + connection.URL)
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

### Node Scoring (Within the Slot Set)

Within the N-node candidate set, the client picks the single best node using a multiplicative score:

```
score = rttBucket * max(affinityCounter, 1.0) * shardCostMultiplier
```

Where `affinityCounter` is the time-weighted EWMA counter (see [Two Decay Counters](#two-decay-counters)). This is distinct from the per-index fan-out counter; scoring uses the per-connection counter that accumulates CPU microseconds and decays based on wall-clock time.

Lower score wins. The client selects exactly one connection: the candidate with the lowest score. The fan-out set is a _consideration pool_, not a round-robin target. After each request, the winning node's counter increases by the request's estimated CPU cost, so subsequent requests may select a different winner from the same candidate set. Load distributes through score-driven rebalancing, not rotation.

The score uses the quantized **RTT bucket** rather than raw RTT. Bucketing absorbs jitter within a network tier: two same-AZ nodes at 180 us and 220 us both land in bucket 8 and compete purely on counter and shard cost multiplier. Raw microseconds would introduce spurious differentiation between peers in the same tier.

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
    Network tier         Typical RTT     Bucket    Ratio vs same-AZ
    ──────────────────   ────────────    ──────    ──────────────────
    Same rack            ~100 us         8         1x       <- floor clamps to 8
    Same AZ              ~200 us         8         1x       <- floor clamps to 8
    Cross-AZ (near)      ~1 ms           9         1.125x
    Cross-AZ (far)       ~3 ms           11        1.375x
    Cross-region (near)  ~20 ms          14        1.75x
    Cross-region (far)   ~80 ms          16        2x


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
    all land in bucket 8, so they compete on counter and shard cost
    alone. Without the floor, a 100us node would get bucket 6, creating
    spurious differentiation between same-AZ peers.
```

The bucket value provides cold-start and idle-state preference for local nodes (lower RTT produces a lower score when counters are equal). However, at sustained load, the **tier-span equalization** mechanism ensures equal traffic distribution across all active tiers. See [Tier-Span Equalization](#steady-state-traffic-distribution-tier-span-equalization) for details.

When only local nodes are active (single-tier operation), the RTT bucket differentiates among local peers via counter and shard cost alone. The bucket's role reduces to a tiebreaker when equalization is not in effect.

#### Two Decay Counters

The system uses two distinct decay counters for different purposes:

##### Per-Connection Scoring Counter (`timeWeightedCounter`)

Each connection has a time-weighted EWMA counter used in the scoring formula. This counter decays continuously based on wall clock time, not request frequency:

```
counter = counter * e^(-lambda * dt) + cpuMicros
```

Where `dt` is seconds since last update and `lambda = ln(2) / halfLife` (default halfLife = 5 s, so lambda ~= 0.1386). The value added is not a fixed `+1`; it is the estimated server-side CPU time in microseconds:

```
cpuMicros = (requestDuration - healthCheckRTT) / allocatedProcessors
```

For a typical 10 ms search on a 4-core node with 1 ms RTT: `(10000 - 1000) / 4 = 2250` microseconds per request. At steady state with rate R requests per second:

```
steady_state ~= cpuMicros * R / lambda ~= 7.2 * cpuMicros * R
```

For 100 req/s at 2250 us per request: `7.2 * 2250 * 100 ~= 1,620,000`.

Properties:

- **Time-decoupled** -- Idle nodes drain regardless of request rate. Half the value decays in 5 seconds; 99% decays in approximately 33 seconds.
- **Cost-weighted** -- Expensive queries (large aggregations) accumulate load faster than inexpensive queries (point lookups).
- **No resets required** -- Old load fades naturally via continuous exponential decay; no sawtooth patterns from periodic clears.
- **Lock-free** -- Atomic CAS loop on `float64` bits stored in a `uint64`, with the timestamp in a separate atomic.

##### Per-Index Fan-Out Counter (`decayCounter`)

Each index slot has a simple decay counter used _only_ for dynamic fan-out sizing, not for scoring:

```
counter = counter * 0.999 + 1.0
```

This counter increments by exactly 1.0 per request, with per-request multiplicative decay. At constant rate it converges to `1/(1-0.999) = 1000`. The fan-out calculation divides this counter by `fanOutPerRequest` (default 500) to determine how many extra nodes to include: `rateFanOut = int(counter / 500) + 1`.

This counter is not involved in scoring or node selection; it controls only the size of the candidate set.

#### CPU-Time Attribution

The per-connection scoring counter accumulates estimated CPU time in microseconds: `(requestDuration - healthCheckRTT) / allocatedProcessors`. The health-check RTT (a near-zero-CPU `GET /`) approximates wire time, so the difference isolates on-CPU processing time. Dividing by processor count normalizes for node capacity. This provides a cost-weighted load signal: a node handling expensive aggregation queries accumulates load faster than one serving point lookups.

When tier-span equalization is active (smoothedMaxBucket > 1), the cost added to the counter is inflated before accumulation:

```
cost = cpuMicros * (smoothedMaxBucket / thisBucket)
```

Where `thisBucket` is the connection's own RTT bucket. This inflation ensures the scoring formula's `rttBucket` multiplier is cancelled at equilibrium, producing equal per-tier traffic distribution. See [Tier-Span Equalization](#steady-state-traffic-distribution-tier-span-equalization) for the full derivation.

**Caveat**: not all measured CPU time is attributable to the specific node the client communicates with. When a node acts as a coordinator (which occurs for any request where the receiving node does not host all relevant shards), it proxies sub-requests to other data nodes. The measured request duration includes both the coordinator's own work and time spent waiting for those sub-requests. The client cannot decompose this; it cannot distinguish "this node spent 10 ms on CPU" from "this node spent 2 ms coordinating and 8 ms waiting for a shard node."

This is acceptable for two reasons:

1. **Directional correctness** -- A node handling more coordinating work _is_ busier from this client's perspective. The counter reflects the total cost of interacting with that node, which is the appropriate signal for load-shedding decisions.
2. **Complementary with affinity routing** -- When shard-aware routing is effective, most requests reach nodes that host the target shards, minimizing coordinator overhead. The CPU-time attribution is most accurate precisely when affinity routing is working correctly. When affinity routing degrades (stale shard maps, missing `/_cat/shards` data), the shard cost falls back to the unknown value (32x) and scoring degrades to RTT-only, where the attribution caveat is irrelevant.

#### Shard Cost Multiplier (Operation-Aware)

When `/_cat/shards` data is available, the client knows whether each node hosts primary shards, replica shards, or both for the target index. The score includes a `shardCostMultiplier` that biases selection based on both the node's shard composition and the operation type (read vs write).

Each affinity wrapper is constructed with a cost table selected at policy construction time:

**Read routes** (search, document get, snapshots) -- `shardCostForReads`:

| Shard Role                | Cost | Notes                                                  |
| ------------------------- | ---- | ------------------------------------------------------ |
| Replica-only              | 1.0  | Preferred: lock-free Lucene snapshot reads             |
| Mixed (primary + replica) | 1.0  | Can serve reads locally; CPU counter differentiates    |
| Primary-only              | 2.0  | Primaries contend with writes                          |
| Unknown                   | 32.0 | No shard data; heavily penalized to prefer known nodes |

**Write routes** (bulk, index, update, delete) -- `shardCostForWrites`:

| Shard Role                | Cost | Notes                                                  |
| ------------------------- | ---- | ------------------------------------------------------ |
| Primary-only              | 1.0  | Preferred: writes go to primary shard first            |
| Mixed (primary + replica) | 1.0  | Can serve writes locally; CPU counter differentiates   |
| Replica-only              | 2.0  | Replica must proxy to primary -- coordinator hop       |
| Unknown                   | 32.0 | No shard data; heavily penalized to prefer known nodes |

Mixed nodes (hosting both primaries and replicas for the target index) get `min(replica, primary)` cost -- 1.0 for both tables -- since they can serve both reads and writes locally. Load-based differentiation between mixed nodes is handled by the CPU-time decay counter, not by this multiplier.

When `/_cat/shards` data is unavailable, all nodes receive the unknown penalty (32x). Since this applies uniformly, it does not affect relative ordering: scoring degrades to RTT \* counter only, and the penalty cancels out in comparison.

#### Score Components Visualization

The three multiplicative components interact to create a composite score that ranks every candidate connection:

```
    score = rttBucket * max(affinityCounter, 1.0) * shardCostMultiplier
            ────┬───   ──────────┬──────────────   ──────────┬─────────
                │                │                           │
                │                │                           +-- Shard cost (operation-aware)
                │                │                               reads: replica=1.0  primary=2.0
                │                │                               writes: primary=1.0  replica=2.0
                │                │                               unknown=32.0 (both)
                │                │
                │                +-- CPU-weighted load (time-decaying EWMA)
                │                    idle=1.0  (halfLife=5s, accumulates CPU microseconds)
                │
                +-- Network proximity (stable, power-of-two buckets)
                    same-AZ=8  cross-AZ=9-11  cross-region=14-16


    Example: 6-node cluster, index "orders" with fan-out K=4 (read path)
    (counter values reflect time-weighted CPU microsecond accumulation)

    Node       AZ     RTT Bucket  Counter    Shard Cost      Score       Fan-out?  Rank
    ---------  -----  ----------  ---------  --------------  ----------  --------  ----
    node-1     us-e1  8           12,500     1.0 (replica)   100,000     K=4       3
    node-2     us-e1  8           8,200      2.0 (primary)   131,200     K=4       4
    node-3     us-e2  11          1.0        1.0 (replica)   11          K=4       1 <- winner
    node-4     us-e2  11          1.0        2.0 (primary)   22          K=4       2
    node-5     us-e3  14          1.0        1.0 (replica)   --          --        --
    node-6     us-e3  14          1.0        32.0 (unknown)  --          --        --

    Fan-out K=4 selected [node-1, node-2, node-3, node-4] via slot filling.
    node-5 and node-6 are not in the fan-out set and are not scored.
    node-3 wins this request (lowest score among the 4 candidates).
```

The score is purely ordinal -- the client picks the lowest. What matters is relative ranking, not absolute magnitude.

### AZ-Aware Overflow

The multiplicative score formula creates natural tier-based overflow. Because all candidates in the fan-out set are scored on every request, traffic splits across RTT tiers almost immediately. This is **not** a "fill the local tier to saturation, then overflow" model.

#### How Fast Does Crossover Happen?

The crossover between a local node (bucket=8) and a remote node (bucket=B) occurs when their scores equalize:

```
local_score  = 8 * counter_local
remote_score = B * 1.0            (idle remote, counter floored at 1.0)
crossover when: counter_local = B / 8
```

Because the scoring counter adds CPU microseconds (not a fixed `+1`), a single request to the local node pushes the counter well past any realistic bucket value. For a typical 10 ms search on a 4-core node with 1 ms RTT:

```
cpuMicros = (10000 - 1000) / 4 = 2250

After 1st request to local node:
  local_score  = 8 * 2250 = 18000
  remote_score = 11 * 1.0 = 11       <- remote wins immediately
```

**Crossover occurs after the first request** -- not after dozens of requests or at saturation. The CPU-microsecond scale of the counter means the bucket ratio (11:8) is small compared to the per-request cost (2250 us). The remote node is competitive from the first routing decision.

```
    Score after the first request -- local (bucket=8) vs remote (bucket=11)
    (10ms search, 4-core node, 1ms RTT; ignoring shard cost)

    score (log scale)
   18000 │ ●                                   local: 8 * counter
         │  ╲
         │   ╲╮ time decay (halfLife=5s)
    9000 │    ╰╲
         │      ╲                              local counter decays toward 0
         │       ╲╮                              between requests
    4000 │        ╰╲
         │          ╲
     500 │            ╲─ ─ ─ ─ ─ ─ ─ ─ ─       if idle long enough, local
         │                                       regains advantage
      11 │━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  remote: 11 * 1.0
       8 │
       0 └──────────────────────────────────────── time
         req#1   +1s      +3s      +5s     +10s

    The local node's score jumps above the remote on the first request,
    then decays back. Traffic interleaves immediately.
```

#### Steady-State Traffic Distribution (Tier-Span Equalization)

At sustained load across multiple RTT tiers, the **tier-span equalization** mechanism produces equal per-tier traffic distribution. The key insight: without equalization, `score = rttBucket x counter` creates a permanent traffic ratio locked to the bucket gap (for example, 11:8 for bucket 8 versus bucket 11). This overloads local nodes and underutilizes remote ones.

Tier-span equalization inflates the cost added to each connection's counter at attribution time:

```
inflated_cost = cpuMicros x (smoothedMaxBucket / thisBucket)
```

Where `smoothedMaxBucket` is the MIAD-smoothed highest RTT bucket across all candidates in the index's fan-out set, and `thisBucket` is the connection's own RTT bucket.

**Why this produces equal distribution:**

At equilibrium, scores equalize across tiers:

```
score = rttBucket x counter x shardCostMultiplier

counter is proportional to rate x cpuMicros x (smoothedMaxBucket / thisBucket) / lambda

Substituting into score:
  score = rttBucket x rate x cpuMicros x smoothedMaxBucket / (thisBucket x lambda)

Since rttBucket == thisBucket for each connection:
  score = rate x cpuMicros x smoothedMaxBucket / lambda

The rttBucket term cancels. Equal scores imply equal rates across all tiers.
```

The result: 2 active tiers -> 50/50 split, 3 tiers -> 33/33/33, 4 tiers -> 25/25/25/25.

```
    Equilibrium traffic distribution: 6-node fan-out set across 3 AZs
    (tier-span equalization active, smoothedMaxBucket = 20)

    reqs/s
     40 │ ██ ██              ██ ██         ██ ██
        │ ██ ██              ██ ██         ██ ██
     30 │ ██ ██              ██ ██         ██ ██
        │ ██ ██              ██ ██         ██ ██     Equal per-tier:
     20 │ ██ ██              ██ ██         ██ ██       AZ-1: 33+33 = 66 (33%)
        │ ██ ██              ██ ██         ██ ██       AZ-2: 33+33 = 66 (33%)
     10 │ ██ ██              ██ ██         ██ ██       AZ-3: 33+33 = 66 (33%)
        │ ██ ██              ██ ██         ██ ██
      0 └──────────────────────────────────────────
        n1     n2           n3     n4     n5     n6
        ──── AZ-1 ────     ──── AZ-2 ──  ── AZ-3 ──
        (bucket=8)         (bucket=11)    (bucket=14)

    Within each AZ, nodes split evenly (same bucket, so only counter
    and shard cost differentiate). Primary-hosting nodes receive
    slightly less traffic on read routes due to the shard cost multiplier
    (not shown here for clarity).
```

#### Cold-Start and Idle Behavior

When traffic is low or absent, counters decay to the floor (1.0) and the `rttBucket` multiplier alone determines ranking. Local nodes are therefore preferred during cold start: the RTT bucket provides a natural tiebreaker. As load increases and the fan-out set spans multiple tiers, tier-span equalization activates and distributes traffic equally.

This two-phase behavior is intentional:

1. **Cold start / light load**: RTT-based preference keeps traffic local, benefiting from warm TCP connections and filesystem page cache.
2. **Sustained load**: Equal per-tier distribution prevents local-node saturation and ensures all tiers contribute equally to throughput.

The transition is smooth rather than step-wise because the `smoothedMaxBucket` value is MIAD-smoothed (see below).

#### MIAD Smoothing of smoothedMaxBucket

The `smoothedMaxBucket` value on each index slot is not set directly to the observed max bucket -- it is smoothed using **Multiplicative Increase, Additive Decrease (MIAD)**:

- **Multiplicative Increase (MI)**: When the observed max bucket exceeds the current smoothed value (demand is growing), the smoothed value converges exponentially toward the observation with a half-life of 2 seconds. After one half-life, approximately 50% of the gap is closed; after three half-lives (6 s), approximately 87.5%.

- **Additive Decrease (AD)**: When the observed max bucket drops below the smoothed value (demand is shrinking), the smoothed value decreases linearly at 0.03 buckets per second. This gradual cooldown prevents premature contraction and keeps connections warm.

```
    smoothedMaxBucket over time: demand spike then gradual recovery

    bucket
     11 │         ╭─────  MI: fast convergence (2s half-life)
        │        ╱
     10 │      ╱
        │    ╱
      9 │  ╱
        │╱
      8 │──────╲───────────────────────────────────────  floor
        │        ╲   AD: slow linear decrease (0.03 buckets/sec)
        │          ╲────────────────
      0 └───────────────────────────────────────────────── time
        demand  +2s    +6s      +30s     +60s     +100s
        spike

    Growth is fast (MI) because slow growth penalizes requests during
    ramp-up. Decrease is slow (AD) to keep connections warm and avoid
    premature contraction that would cause oscillation.
```

The MIAD asymmetry is the inverse of TCP's AIMD (Additive Increase, Multiplicative Decrease). TCP grows slowly to probe capacity and backs off fast on congestion. Here, the client grows fast because the penalty for slow growth is degraded SLOs during demand ramp-up, and decreases slowly because the penalty for over-provisioning remote tiers is only marginally elevated cost (remote connections remain warm for free via TCP keepalive).

The smoothed value is floored at 1.0 (single-tier baseline) and clamped to never overshoot below the observed value during AD.

#### Zero-Allocation Read-at-Attribution-Time

The `smoothedMaxBucket` is read from the index slot at CPU-time attribution (after the request completes), not curried through the request context. This avoids per-request `context.WithValue` allocations.

The transport reads the smoothed value via a `tierSpanProvider` interface assertion on the router:

```go
if tsp, ok := c.router.(tierSpanProvider); ok {
    smoothedMaxBucket = tsp.smoothedMaxBucketForIndex(indexName)
}
conn.recordCPUTime(dur, smoothedMaxBucket)
```

This is valid because `smoothedMaxBucket` is a property of the index slot, not the individual request, and changes slowly (MIAD-smoothed). Reading it a few milliseconds after the Eval() call produces the same value in practice.

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

### Preference Injection

When a `Router` is configured and `SkipAffinityPreferLocal` is `false` (the default), the client adds `?preference=_local` to all outgoing requests that do not already have a `preference` parameter. This tells OpenSearch to prefer shard copies on the receiving node, complementing the client-side affinity routing with server-side shard selection.

If the request already has a `preference` parameter (set by the caller), the client does not override it.

Note: preference injection applies whenever any router is set (including `NewRoundRobinRouter` and `NewMuxRouter`), not only with `NewSmartRouter`.

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

Between discovery cycles, the shard placement map can become stale. A node may flap (fail, have its shards relocated, then recover), or a shard may be reinitializing or relocating. When this occurs, the client routes requests to a node that no longer hosts the target shards, forcing the server to act as a coordinator proxy -- exactly the overhead that affinity routing is designed to eliminate.

The client detects this situation reactively: when a request to a node fails (transport error or retryable HTTP status), the failing connection is marked with a `needsCatUpdate` flag. This flag has three effects:

1. **Exclusion from affinity routing** -- `rendezvousTopK` filters out connections with `needsCatUpdate`. The node is excluded from all index candidate sets until the flag is cleared. Round-robin fallback and zombie tryout routing still include the node, so it remains reachable for non-affinity traffic.

2. **Survives resurrection** -- Unlike the dead/alive lifecycle, `needsCatUpdate` is an independent metadata bit that persists through resurrection. A node can pass health checks, return to the active pool, and serve round-robin requests, but it remains excluded from affinity candidate sets until shard placement is reconfirmed. This is the essential design point: the shard map for a resurrected node is not trusted until ground truth from `/_cat/shards` is obtained.

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

When the `/_cat/shards` fetch completes successfully, the `needsCatUpdate` flag is cleared on all connections and any pending expedited refresh timer is cancelled. Connections re-enter affinity candidate sets with fresh shard placement data.

**Design note**: a server can fail and be resurrected without the client observing the event, and this is intentional. The `needsCatUpdate` flag is set only when the client _observes_ a failure on a connection it is actively using. If a node flaps but the client has no in-flight requests to that node, the topology change is discovered on the next regular discovery cycle. The client reacts proportionally to the failures it experiences, not to cluster events it cannot observe.

#### Observer Hook

Implement `OnShardMapInvalidation(event ShardMapInvalidationEvent)` on your `ConnectionObserver` to monitor when connections are flagged. The event includes the connection URL, name, reason (`"transport_error"` or `"http_status_retry"`), and timestamp.

## Configuration

### Functional Options

```go
router := opensearchtransport.NewSmartRouter(
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

    // Set to true to disable automatic ?preference=_local injection.
    // Default: false (preference is injected when any Router is set).
    SkipAffinityPreferLocal: false,
})
```

### Option Reference

| Option                    | Default | Description                                                              |
| ------------------------- | ------- | ------------------------------------------------------------------------ |
| `WithMinFanOut(n)`        | 1       | Minimum nodes in an index slot. Floor for fan-out.                       |
| `WithMaxFanOut(n)`        | 32      | Maximum nodes in an index slot. Caps pathologically sharded indexes.     |
| `WithDecayFactor(d)`      | 0.999   | Fan-out counter decay factor. Must be in (0, 1). Higher = longer memory. |
| `WithFanOutPerRequest(f)` | 500     | Decay counter value that maps to +1 fan-out node.                        |
| `WithIdleEvictionTTL(d)`  | 90 min  | How long an idle index slot persists before eviction.                    |
| `WithIndexFanOut(m)`      | nil     | Per-index fan-out overrides. Bypasses dynamic calculation.               |
| `SkipAffinityPreferLocal` | false   | When true, skips `?preference=_local` injection.                         |

## Architecture Diagram

```
                    ┌──────────────────────────────────────────────────────┐
                    │                    Client.Perform(req)               │
                    │                                                      │
                    │  1. injectPreference(req)   // ?preference=_local    │
                    │  2. router.Route(ctx, req)                           │
                    └────────────────────┬─────────────────────────────────┘
                                         │
                    ┌────────────────────▼─────────────────────────────────┐
                    │              PolicyChain                             │
                    │                                                      │
                    │  ┌─────────────────────────────────────────────┐     │
                    │  │ CoordinatingPolicy (if coordinating nodes)  │     │
                    │  └─────────────────┬───────────────────────────┘     │
                    │                    │ fall through                    │
                    │  ┌─────────────────▼───────────────────────────┐     │
                    │  │ MuxPolicy (HTTP pattern matching)           │     │
                    │  │                                             │     │
                    │  │  POST /{index}/_search -> affinityWrapped(  │     │
                    │  │    search -> data -> null)                  │     │
                    │  │  POST /_bulk -> affinityWrapped(ingest)     │     │
                    │  │  ...                                        │     │
                    │  └─────────────────┬───────────────────────────┘     │
                    │                    │ fall through                    │
                    │  ┌─────────────────▼───────────────────────────┐     │
                    │  │ RoundRobinPolicy (fallback)                 │     │
                    │  └─────────────────────────────────────────────┘     │
                    └──────────────────────────────────────────────────────┘

                    ┌────────────────────────────────────────────────────────┐
                    │           affinityPolicyWrapper.Eval(req)              │
                    │                                                        │
                    │  1. inner.Eval(req) -> multiServerPool                 │
                    │  2. Copy active connections from pool (RLock)          │
                    │  3. Sort by RTT bucket (insertion sort, O(n))          │
                    │  4. Extract index (+ docID) from request path          │
                    │  5. Look up index slot -> fan-out, shard nodes         │
                    │  6. rendezvousTopK(key, conns, fanOut, shardNodes)     │
                    │     a. Partition: shard-hosting vs non-shard nodes     │
                    │     b. Fill K slots tier-by-tier (nearest RTT first)   │
                    │     c. Rendezvous hash within each tier                │
                    │     d. Jitter-rotate the K-slot result                 │
                    │  7. Score candidates: RTT x cpuCounter x shardCost     │
                    │  8. Pick lowest score -> record CPU time               │
                    │  9. Return affinityPool{conn: best}                    │
                    └────────────────────────────────────────────────────────┘
```

## Key Properties

| Property                     | Mechanism                                                                                                                                     |
| ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| Cache locality               | Rendezvous hash routes same index to same nodes                                                                                               |
| AZ preference                | RTT bucket scoring: local nodes score lower                                                                                                   |
| Shard cost (operation-aware) | Shard cost multiplier: reads prefer replicas, writes prefer primaries                                                                         |
| Load shedding                | Time-weighted CPU counter: busy nodes score higher, traffic overflows                                                                         |
| No sawtooth                  | Continuous time-based decay: no periodic resets needed                                                                                        |
| Fleet symmetry breaking      | Per-client jitter rotation within slot set                                                                                                    |
| Hot index scaling            | Dynamic fan-out from shard count + request rate                                                                                               |
| Cold index eviction          | idleSince + 90min TTL prevents cache bloat                                                                                                    |
| Graceful degradation         | Non-index requests and unhealthy pools fall through to round-robin                                                                            |
| Stale shard recovery         | `needsCatUpdate` excludes failed connections from affinity until `/_cat/shards` confirms                                                      |
| Lock-free fast path          | RTT ring reads, affinity counter CAS, atomic fan-out reads -- no mutexes on the request path except a brief RLock to copy the connection list |

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
affinity_routing:
  cluster_permissions:
    - "cluster:monitor/shards"
```

**OpenSearch < 2.17 (or mixed-version deployments):**

```yaml
affinity_routing:
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
affinity_routing:
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
PUT /_plugins/_security/api/roles/affinity_routing
{
  "cluster_permissions": ["cluster_monitor"],
  "index_permissions": [{
    "index_patterns": ["*"],
    "allowed_actions": ["indices_monitor"]
  }]
}
```

## Comparison with Other Routers

| Feature              | Round-Robin | Mux Router           | Smart Router (Affinity)                             |
| -------------------- | ----------- | -------------------- | --------------------------------------------------- |
| Node selection       | Round-robin | Role-based           | Role-based + index-consistent                       |
| Cache locality       | None        | None                 | Per-index node affinity via rendezvous hash         |
| AZ awareness         | None        | None                 | RTT-based scoring with tier overflow                |
| Shard cost           | None        | None                 | Operation-aware shard cost from `/_cat/shards` data |
| Load balancing       | Uniform     | Uniform within role  | CPU-cost-weighted within slot                       |
| Shard awareness      | None        | None                 | Hard partition for shard-hosting nodes              |
| Fleet distribution   | None        | None                 | Per-client jitter rotation                          |
| Configuration        | None        | None                 | Optional (good defaults)                            |
| Per-request overhead | O(1)        | O(1) per route match | O(K) per request (K = fan-out)                      |

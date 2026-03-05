# Life of a Request

This guide traces a request from the Go client API call through the routing pipeline to connection selection. It covers what happens at each step, how the scoring formula works, and why the overhead is negligible compared to network latency.

For background on the routing system, see [Request Routing](request_routing.md). For scoring configuration and tuning, see [Connection Scoring](connection_scoring.md).

## Overview

Every request follows the same path through the transport layer:

```
client.Search(ctx, ...)
  |
  v
transport.Perform(req)
  |
  +-- router.Route(ctx, req)                    -- policy chain evaluation
  |     |
  |     +-- IfEnabledPolicy                     -- branch on coordinating-only node availability
  |     |     +-- RolePolicy(CoordinatingOnly)  -- short-circuit if coordinating-only nodes exist
  |     +-- MuxPolicy                           -- trie lookup by method + path
  |     |     +-- routeTrie.match(method, path) -- zero-alloc segment matching
  |     |     +-- poolRouter.Eval()             -- connection scoring over role pool
  |     +-- RoundRobinPolicy                    -- fallback if no route matched
  |
  +-- conn.addInFlight(hop.PoolName)            -- per-pool in-flight tracking
  +-- http.Transport.RoundTrip(req)             -- network I/O
  +-- conn.releaseInFlight(hop.PoolName)        -- decrement in-flight
  +-- conn.recordCPUTime(duration)              -- update estimated server-side load
  +-- router.OnSuccess(conn) / OnFailure(conn)  -- connection health bookkeeping
```

## Policy Chain Evaluation

The policy chain tries each sub-policy in order. The first to return a `NextHop` with a non-nil connection wins.

| Step | Policy             | Cost (ns) | What happens                                                                                                                                                                  |
| ---- | ------------------ | --------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1    | `IfEnabledPolicy`  | ~151      | If coordinating-only nodes exist (via `RolePolicy(RoleCoordinatingOnly)`), routes all traffic to them exclusively. Otherwise evaluates the MuxPolicy + RoundRobin branch.     |
| 2    | `MuxPolicy`        | ~181      | Trie lookup classifies the request by method + path into one of 11 route categories. Each category delegates to a role pool wrapped in a `poolRouter` for connection scoring. |
| 3    | `RoundRobinPolicy` | ~154      | Fallback for requests that don't match any MuxPolicy route. Simple weighted round-robin across all non-manager nodes.                                                         |

When `MuxPolicy` matches, the `poolRouter.Eval()` adds connection scoring on top of the role pool selection. This is where the interesting routing decisions happen.

## Route Classification

The trie maps ~124 URL patterns to 11 route categories. Each category has a role chain, a thread pool name (for per-pool congestion tracking), and a shard cost table (read vs write).

| Category       | Example endpoints                               | Role chain             | Pool name     | Cost table |
| -------------- | ----------------------------------------------- | ---------------------- | ------------- | ---------- |
| searchRead     | `_search`, `_msearch`, `_count`, `_validate`    | search -> data -> null | `search`      | reads      |
| getRead        | `_doc/{id}`, `_source/{id}`, `_mget`            | search -> data -> null | `get`         | reads      |
| dataWrite      | `_doc/{id}` (PUT/POST), `_create`, `_update`    | data -> null           | `write`       | writes     |
| ingestWrite    | `_bulk`, `_reindex`                             | ingest -> null         | `write`       | writes     |
| dataRefresh    | `_refresh`                                      | data -> null           | `refresh`     | writes     |
| dataFlush      | `_flush`                                        | data -> null           | `flush`       | writes     |
| dataForceMerge | `_forcemerge`                                   | data -> null           | `force_merge` | writes     |
| dataMgmt       | `_recovery`, `_shard_stores`, `_stats`          | data -> null           | `management`  | reads      |
| searchMgmt     | `_field_caps`                                   | search -> data -> null | `management`  | reads      |
| ingestMgmt     | `_ingest/pipeline`                              | ingest -> null         | `management`  | reads      |
| warmMgmt       | `_snapshot/{repo}/_mount`, `/{index}/_settings` | warm -> data -> null   | `management`  | reads      |

Role chains use `IfEnabledPolicy` for fallback: if dedicated search nodes exist, use them; otherwise fall to data nodes; otherwise null (no match, falls through to round-robin).

## Shard Cost Tables

Different operations use different cost tables based on the read/write direction. Lower cost = preferred node.

### Read Operations

Reads prefer replica-hosting nodes. Replicas serve reads from a lock-free Lucene snapshot that does not contend with write indexing.

| Shard state  | Cost | Reason                                                      |
| ------------ | ---- | ----------------------------------------------------------- |
| Replica      | 1.0  | Preferred -- lock-free Lucene snapshot, no write contention |
| Primary      | 2.0  | Acceptable -- contends with concurrent writes               |
| Relocating   | 8.0  | Shard moving, may require proxy hop                         |
| Initializing | 16.0 | Shard not yet ready to serve                                |
| Unknown      | 32.0 | No shard data from discovery yet                            |

### Write Operations

Writes prefer primary-hosting nodes. Writes always go to the primary shard first; routing to a replica-only node forces a coordinator proxy hop.

| Shard state  | Cost | Reason                                           |
| ------------ | ---- | ------------------------------------------------ |
| Primary      | 1.0  | Preferred -- write lands directly on the primary |
| Replica      | 2.0  | Must proxy through primary -- adds a hop         |
| Relocating   | 8.0  | Shard moving, may require proxy hop              |
| Initializing | 16.0 | Shard not yet ready to serve                     |
| Unknown      | 32.0 | No shard data from discovery yet                 |

Nodes hosting both primaries and replicas for the target index get the better (lower) of the two costs.

## The Scoring Formula

Once the candidate set is determined (via rendezvous hash or shard-exact lookup), each candidate is scored:

```
score = rttBucket * (inFlight + 1) / cwnd * shardCost
```

**Lower score = preferred node.** The three factors:

| Factor                  | Source                          | What it measures                                                                                                                                                                    |
| ----------------------- | ------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `rttBucket`             | `conn.rttRing.medianBucket()`   | Network proximity. Power-of-2 quantized health check RTT (floor 8 = 256 us). Same-AZ nodes typically bucket 8-9; cross-AZ nodes bucket 10-11.                                       |
| `(inFlight + 1) / cwnd` | Per-pool AIMD congestion window | Utilization ratio. `inFlight` is client-tracked per thread pool. `cwnd` is adjusted by the stats poller via AIMD (additive increase, multiplicative decrease on rejected requests). |
| `shardCost`             | Shard cost table lookup         | Whether the node hosts relevant shard data for the target index. See tables above.                                                                                                  |

When a thread pool is overloaded (rejected requests or HTTP 429), the score is `math.MaxFloat64` -- the node is skipped entirely for that pool.

### Concrete Scoring Example

Three-node cluster, search request for `my-index`:

```
Node A: bucket=8,  inFlight=2, cwnd=13, replica  (read cost 1.0)
  score = 8 * (2+1)/13 * 1.0 = 1.85

Node B: bucket=8,  inFlight=0, cwnd=13, primary  (read cost 2.0)
  score = 8 * (0+1)/13 * 2.0 = 1.23

Node C: bucket=11, inFlight=0, cwnd=13, replica   (read cost 1.0)
  score = 11 * (0+1)/13 * 1.0 = 0.85
```

Winner: **Node C** (score 0.85). Despite being cross-AZ (higher RTT bucket), its zero in-flight and preferred shard role make it the best choice. The congestion factor `(inFlight + 1) / cwnd` naturally balances load: as Node C accumulates in-flight requests, its score rises and traffic shifts to Nodes A and B.

## Request Flow: Search

```
POST /my-index/_search
  |
  v
MuxPolicy: routeTrie.match("POST", "/my-index/_search")
  |  -> leaf: { policy=poolRouter(searchRead), pool="search" }
  |
  v
poolRouter.Eval()
  |
  +-- inner.Eval() -> default connection from search/data role pool
  +-- extractIndexFromPath("/my-index/_search") -> "my-index"
  +-- extractDocumentFromPath() -> ("", "") -- not a document endpoint
  +-- cache.getOrCreate("my-index") -> indexSlot
  +-- extractRouting(req) -> "" (no ?routing=)
  +-- effectiveRoutingKey = "" (no docID either)
  +-- shardExactCandidates() -> nil (no effective routing key)
  +-- effectiveFanOut(slot, "my-index", nodeCount) -> K
  +-- rendezvousTopK("my-index", "", conns, K, ...)
  |     +-- partition: shard-hosting nodes first
  |     +-- fill by RTT tier, hash within tier
  |     +-- rotate by jitter
  +-- connScoreSelect(candidates, slot, nil, shardCostForReads, "search", ...)
  |     +-- score = rttBucket * (inFlight+1)/cwnd * shardCost
  +-- return NextHop{Conn: best, PoolName: "search"}
  |
  v
conn.addInFlight("search") -> HTTP -> conn.releaseInFlight("search")
```

The rendezvous hash path is the common case for search. Fan-out K determines the candidate set size (driven by request volume and shard placement). Within the K candidates, the scoring formula picks the least-loaded node with the best shard affinity.

## Request Flow: Document Get

```
GET /my-index/_doc/abc123
  |
  v
MuxPolicy: routeTrie.match("GET", "/my-index/_doc/abc123")
  |  -> leaf: { policy=poolRouter(getRead), pool="get" }
  |
  v
poolRouter.Eval()
  |
  +-- inner.Eval() -> default connection from search/data role pool
  +-- extractIndexFromPath("/my-index/_doc/abc123") -> "my-index"
  +-- extractDocumentFromPath("/my-index/_doc/abc123") -> ("my-index", "abc123")
  +-- cache.getOrCreate("my-index") -> indexSlot
  +-- extractRouting(req) -> "" (no ?routing=)
  +-- effectiveRoutingKey = "abc123" (docID fallback -- OpenSearch default)
  +-- shardExactCandidates(features, slot, "abc123", conns)
  |     +-- murmur3("abc123") -> shard N
  |     +-- lookup shard N -> primary + replica node names
  |     +-- filter to connections matching those nodes
  +-- connScoreSelect(shardCandidates, slot, shard, shardCostForReads, "get", ...)
  |     +-- forShard(): per-shard primary/replica cost (not aggregate per-node)
  |     +-- score = rttBucket * (inFlight+1)/cwnd * shardCost
  +-- return NextHop{Conn: best, PoolName: "get"}
  |
  v
conn.addInFlight("get") -> HTTP -> conn.releaseInFlight("get")
```

Document gets use **shard-exact routing**. The client computes the target shard number using murmur3 hash (matching the server's `OperationRouting.generateShardId()`), then scores only the 1-3 nodes hosting that shard. This avoids the rendezvous hash fan-out entirely and ensures the request hits a node with the data in its OS page cache.

When the shard map is not yet available (first few seconds after startup), the path falls back to rendezvous hashing.

## Request Flow: Write

```
PUT /my-index/_doc/abc123
  |
  v
MuxPolicy: routeTrie.match("PUT", "/my-index/_doc/abc123")
  |  -> leaf: { policy=poolRouter(dataWrite), pool="write" }
  |
  v
poolRouter.Eval()
  |
  +-- inner.Eval() -> default connection from data role pool
  +-- extractIndexFromPath("/my-index/_doc/abc123") -> "my-index"
  +-- extractDocumentFromPath("/my-index/_doc/abc123") -> ("my-index", "abc123")
  +-- cache.getOrCreate("my-index") -> indexSlot
  +-- extractRouting(req) -> "" (no ?routing=)
  +-- effectiveRoutingKey = "abc123" (docID fallback)
  +-- shardExactCandidates(features, slot, "abc123", conns)
  |     +-- murmur3("abc123") -> shard N
  |     +-- lookup shard N -> primary + replica node names
  |     +-- filter to connections matching those nodes
  +-- connScoreSelect(shardCandidates, slot, shard, shardCostForWrites, "write", ...)
  |     +-- forShard(): primary cost 1.0, replica cost 2.0
  |     +-- score = rttBucket * (inFlight+1)/cwnd * shardCost
  |     +-- prefers the primary-hosting node (cost 1.0 vs 2.0)
  +-- return NextHop{Conn: best, PoolName: "write"}
  |
  v
conn.addInFlight("write") -> HTTP -> conn.releaseInFlight("write")
```

Writes use the **write shard cost table**: primary-hosting nodes get cost 1.0 (preferred) because writes always go to the primary shard first. Routing to a replica-only node forces the server to proxy to the primary, adding a hop. The shard-exact path works identically to reads, but the scoring prefers primaries over replicas.

## Request Flow: Cluster Operation

```
GET /_cluster/health
  |
  v
MuxPolicy: routeTrie.match("GET", "/_cluster/health")
  |  -> no match (system endpoints start with _ after /)
  |  -> MuxPolicy returns (NextHop{}, nil) -- no match, try next
  |
  v
RoundRobinPolicy.Eval()
  |  -> weighted round-robin across all non-manager nodes
  |  -> return NextHop{Conn: next}
  |
  v
conn.addInFlight("") -> HTTP -> conn.releaseInFlight("")
```

Cluster operations like `/_cluster/health`, `/_cat/nodes`, and `/_nodes` do not target a specific index. They fall through the MuxPolicy (no route matches) and are handled by the round-robin fallback. This is intentional: these operations are lightweight and can be served by any node.

## Routing Overhead vs Network Latency

All routing decisions are made entirely on the client with zero heap allocations. The overhead is negligible compared to even a single same-AZ network hop.

| Operation      | Router overhead | Same-AZ network RTT   | Overhead      |
| -------------- | --------------- | --------------------- | ------------- |
| Search         | ~259 ns         | 250,000 -- 500,000 ns | 0.05 -- 0.10% |
| Get (doc)      | ~452 ns         | 250,000 -- 500,000 ns | 0.09 -- 0.18% |
| Bulk write     | ~231 ns         | 250,000 -- 500,000 ns | 0.05 -- 0.09% |
| Cluster health | ~187 ns         | 250,000 -- 500,000 ns | 0.04 -- 0.07% |

For cross-AZ traffic (1-2 ms RTT), the overhead drops to 0.01 -- 0.05%.

The routing decision itself costs less than 500 ns while the benefit -- avoiding a coordinator proxy hop -- saves 250,000+ ns. The ratio is roughly 1:500 to 1:4000: for every nanosecond spent on routing, the client saves 500 -- 4000 ns of server-side processing and network transit.

Reference: `opensearchtransport/routing_benchmark_test.go` (Apple M4 Pro, arm64, 2026-03-04). All operations 0 allocs/op.

## Related Guides

- [Request Routing](request_routing.md) -- policy primitives, custom routers, route tables
- [Connection Scoring](connection_scoring.md) -- scoring formula, RTT bucketing, fan-out configuration
- [Connection Pool](connection_pool.md) -- pool structure, lifecycle bits, warmup, standby
- [Node Discovery and Roles](node_discovery_and_roles.md) -- discovery mechanics, role definitions

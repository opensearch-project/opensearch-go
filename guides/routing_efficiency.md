# Routing Efficiency Analysis

This document quantifies the network, availability, and resource improvements that connection scoring provides over traditional round-robin routing. The formulas and examples use representative cluster configurations; substitute site-specific values for production estimates.

## Notation

| Symbol | Meaning                                                               |
| ------ | --------------------------------------------------------------------- |
| N      | Total cluster nodes                                                   |
| n_s    | Nodes hosting shards for a given index                                |
| K      | Fan-out (candidate set size for connection scoring)                   |
| P      | Number of primary shards for an index                                 |
| R      | Replication factor (number of replicas)                               |
| p      | Per-link availability (probability a network link delivers a message) |
| d      | Average response payload size                                         |
| Q      | Request rate (requests/second for an index)                           |
| A      | Number of availability zones                                          |

## Coordinator Hop Elimination

In a standard OpenSearch cluster, any node can serve as the coordinating node for any request. When a client sends a request to a node that does not host the target index's shards, that node acts as a **coordinator proxy**: it forwards the request internally to a shard-hosting node, receives the response, and relays it back to the client. This adds latency, consumes bandwidth on the internal network, and buffers the full response payload in the coordinator's JVM heap.

### Round-Robin (Before)

With round-robin routing, the client picks a node uniformly at random from all N nodes. The probability that the chosen node actually hosts a shard for the target index is:

```
P(direct) = n_s / N
P(proxy)  = 1 - n_s / N
```

### Connection Scoring (After)

With connection scoring, shard-hosting nodes fill the candidate set first (hard partition). When the number of shard-hosting nodes meets or exceeds the fan-out K, all candidates are shard hosts:

```
P(direct) ~= 1.0    when n_s >= K
P(direct) = n_s / K  when n_s < K (scoring further penalizes non-shard nodes 32x)
```

### Hop Elimination Rate

The fraction of requests that no longer require a coordinator proxy hop:

```
eta = 1 - n_s / N
```

This is the single most important metric. It represents the fraction of requests that were paying the coordinator tax under round-robin but now route directly to a shard host.

### Example: Small Cluster (6 nodes)

| Index                    | Shards | Shard Nodes (n_s) | P(proxy) Round-Robin | P(proxy) Scored | eta  |
| ------------------------ | ------ | ----------------- | -------------------- | --------------- | ---- |
| orders (120 GiB, 2Px1R)  | 4      | 4                 | 33%                  | ~= 0%           | 0.33 |
| products (40 GiB, 1Px1R) | 2      | 2                 | 67%                  | ~= 0%           | 0.67 |
| sessions (5 GiB, 1Px1R)  | 2      | 2                 | 67%                  | ~= 0%           | 0.67 |

Even in a small cluster, the median index sees 67% of its requests unnecessarily proxied under round-robin.

**Measured result.** On a 3-node cluster with a 0-replica index (n_s = 1), scored routing directed 100% of 60,000 requests to the single shard-holding node at 320 req/s with 32 concurrent workers. Round-robin would have proxied 67% of those requests. For a 1-replica index (n_s = 2), less than 5% of requests reached the non-shard node.

### Example: Medium Cluster (32 nodes)

| Index                    | Shards | Shard Nodes (n_s) | P(proxy) Round-Robin | P(proxy) Scored | eta  |
| ------------------------ | ------ | ----------------- | -------------------- | --------------- | ---- |
| events (800 GiB, 8Px1R)  | 16     | 14                | 56%                  | ~= 0%           | 0.56 |
| metrics (200 GiB, 4Px1R) | 8      | 8                 | 75%                  | ~= 0%           | 0.75 |
| users (30 GiB, 1Px1R)    | 2      | 2                 | 94%                  | ~= 0%           | 0.94 |
| config (2 GiB, 1Px1R)    | 2      | 2                 | 94%                  | ~= 0%           | 0.94 |

In a 32-node cluster, even the largest index sees more than half its requests proxied. Smaller indexes, which typically constitute the majority, see 94% of requests proxied.

### Example: Large Cluster (256 nodes)

| Index                       | Shards | Shard Nodes (n_s) | P(proxy) Round-Robin | P(proxy) Scored | eta  |
| --------------------------- | ------ | ----------------- | -------------------- | --------------- | ---- |
| logs (4 TiB, 32Px1R)        | 64     | 50                | 80%                  | ~= 0%           | 0.80 |
| analytics (1.5 TiB, 12Px1R) | 24     | 20                | 92%                  | ~= 0%           | 0.92 |
| inventory (300 GiB, 4Px1R)  | 8      | 8                 | 97%                  | ~= 0%           | 0.97 |
| reference (10 GiB, 1Px1R)   | 2      | 2                 | 99%                  | ~= 0%           | 0.99 |

In a large cluster, virtually every request for a typical index passes through a coordinator proxy under round-robin. The hop elimination rate approaches 1.0 for all but the most widely sharded indexes.

## Availability

Each network hop in the request path is a serial failure domain. If each link has independent availability p, the end-to-end availability for a request is p raised to the number of serial links.

### Request Path Comparison

**Round-robin (proxied request)** -- 3 serial link dependencies:

```
client <--> coordinator <--> shard node

A_proxied = p^3
```

**Scored routing (direct to shard host)** -- 2 serial link dependencies:

```
client <--> shard node

A_direct = p^2
```

### Blended Availability for an Index

Under round-robin, some requests hit shard hosts directly (no proxy) and some do not:

```
A_old = (n_s/N) x p^2 + (1 - n_s/N) x p^3
A_new = p^2
```

The availability gain per request:

```
deltaA = p^2 x (1 - n_s/N) x (1 - p)
```

### Worked Examples (p = 0.999)

| Cluster  | Index     | n_s / N | A_old    | A_new    | deltaA    |
| -------- | --------- | ------- | -------- | -------- | --------- |
| 6-node   | products  | 2/6     | 0.997670 | 0.998001 | +0.000331 |
| 32-node  | users     | 2/32    | 0.997065 | 0.998001 | +0.000936 |
| 256-node | reference | 2/256   | 0.997011 | 0.998001 | +0.000990 |
| 256-node | logs      | 50/256  | 0.997198 | 0.998001 | +0.000803 |

The improvement is larger for larger clusters and smaller indexes, which is precisely where the coordinator tax is highest.

### Blast Radius

Under round-robin, a network partition between any two cluster nodes can affect any index's requests because any node may be selected as coordinator. The number of internal links that must be healthy:

```
Links_old = N x (N-1) / 2    (full mesh)
```

Under scored routing, only the links between the client and the n_s shard-hosting nodes matter for a given index:

```
Links_new = n_s
```

| Cluster  | Links (full mesh) | Links (10 GiB index, n_s=2) | Reduction |
| -------- | ----------------- | --------------------------- | --------- |
| 6-node   | 15                | 2                           | 7.5x      |
| 32-node  | 496               | 2                           | 248x      |
| 256-node | 32,640            | 2                           | 16,320x   |

For a small index on a large cluster, the blast radius reduction is dramatic: a network partition between two nodes that do not host the target index has zero impact on that index's requests.

## Bandwidth Savings

Each proxied request carries the response payload across two extra internal network links (coordinator <-> shard node, both directions):

```
BW_overhead_per_request = P(proxy) x 2d
BW_saved_per_request    = eta x 2d
```

### Example: 32-Node Cluster

Assume average response payload d = 8 KiB and 50,000 requests/second across all indexes, with a weighted-average eta of 0.80:

```
Internal BW saved = 50,000 x 0.80 x 2 x 8 KiB = 625 MiB/s
```

This is internal network bandwidth that no longer traverses the cluster fabric. At sustained throughput, this reduction alleviates congestion on the inter-node network and frees capacity for replication traffic, shard recovery, and cluster state propagation.

## Memory Savings

The coordinator node buffers the full response payload in JVM heap before forwarding to the client. Eliminating the proxy hop removes this buffer:

```
RAM_saved_per_concurrent_request = d
RAM_saved_total = concurrent_requests x eta x d
```

### Example

With 2,000 concurrent requests, average response 16 KiB, eta = 0.85:

```
Coordinator heap freed = 2,000 x 0.85 x 16 KiB = 27.2 MiB
```

This is heap space that would otherwise be held in the coordinator's JVM, contributing to garbage collection pressure and circuit-breaker utilization. For aggregation responses (which can be megabytes), the savings are proportionally larger.

## Client-Side Memory Cost

Connection scoring maintains an in-memory index slot cache on the client. The per-index cost:

| Field                         | Size       | Notes                                           |
| ----------------------------- | ---------- | ----------------------------------------------- |
| Index name (string key)       | ~64 B      | Average index name length                       |
| Fan-out, counters, timestamps | ~48 B      | Atomic integers and floats                      |
| Shard placement map           | n_s x 40 B | Node ID (36 B UUID) + shard role byte + padding |
| Rendezvous hash cache         | K x 8 B    | Pre-computed hash weights                       |

**Per-index slot total**: ~200-500 B depending on shard node count.

Per-connection overhead (stored on the connection, not per-index):

| Field              | Size            | Notes                                            |
| ------------------ | --------------- | ------------------------------------------------ |
| RTT ring buffer    | 12 x 8 B = 96 B | Default 12 samples                               |
| Pool registry      | ~80 B per pool  | sync.Map entry + poolCongestion atomics per pool |
| In-flight counters | 4 B per pool    | Atomic int32 per tracked pool                    |

**Per-connection total**: ~120-300 B (depends on number of active thread pools).

### Example: Client-Side RAM by Cluster Size

| Cluster  | Nodes | Indexes | Index Cache | Connection Overhead | Total    |
| -------- | ----- | ------- | ----------- | ------------------- | -------- |
| 6-node   | 6     | 20      | 8 KiB       | 0.7 KiB             | ~9 KiB   |
| 32-node  | 32    | 100     | 40 KiB      | 3.8 KiB             | ~44 KiB  |
| 256-node | 256   | 500     | 200 KiB     | 30 KiB              | ~230 KiB |

Even on a large cluster with 500 indexes and 256 nodes, the total client-side overhead is well under 1 MiB. This is negligible relative to the JVM heap savings from eliminated coordinator buffering.

## Cross-AZ Data Transfer Cost

Cloud providers charge per-GiB for traffic crossing availability-zone boundaries (for example, AWS charges $0.01/GiB in each direction). Coordinator proxying generates cross-AZ traffic whenever the coordinator and shard host reside in different AZs.

### Decomposing Internal Bandwidth

Internal cluster bandwidth has several components:

| Component                                                       | Affected by scored routing?                      |
| --------------------------------------------------------------- | ------------------------------------------------ |
| Coordinator proxy traffic (request forwarding + response relay) | **Yes** -- eliminated for direct-routed requests |
| Shard replication (primary -> replica)                          | No                                               |
| Shard recovery and relocation                                   | No                                               |
| Cluster state propagation                                       | No                                               |
| Node discovery and health checks                                | No                                               |

Scored routing targets the first component. The others are unaffected.

### Coordinator Proxy Cross-AZ Formula

For a cluster spanning A availability zones with roughly equal node distribution:

```
P(cross_AZ_hop) = (A - 1) / A        (probability a random coordinator is in a different AZ than the shard host)
```

For 3 AZs, this is 2/3. The cross-AZ bandwidth from coordinator proxying:

```
BW_proxy_xAZ = Q_total x d_avg x 2 x P(proxy) x P(cross_AZ_hop)
```

Where `Q_total` is aggregate request rate, `d_avg` is average payload size (request + response, whichever dominates), `P(proxy) = 1 - weighted_avg(n_s/N)`, and the factor of 2 accounts for both directions (coordinator<->shard).

Scored routing eliminates the proxy component, so the savings are:

```
BW_saved_xAZ = BW_proxy_xAZ x eta_effective
Cost_saved   = BW_saved_xAZ x seconds_per_month x price_per_GiB x 2
```

The `x 2` accounts for bidirectional charging (in + out).

### Ratio-Based Estimate

Since scored routing affects only coordinator proxy traffic, the savings scale as a fraction of the current proxy-related cross-AZ expenditure. If the coordinator-proxy component of cross-AZ costs can be identified or estimated:

```
Savings = proxy_xAZ_cost x eta_effective
```

With conservative eta = 0.78:

| Current proxy-related cross-AZ cost | Estimated monthly savings |
| ----------------------------------- | ------------------------- |
| $5,000/month                        | ~$3,900/month             |
| $10,000/month                       | ~$7,800/month             |
| $25,000/month                       | ~$19,500/month            |
| $50,000/month                       | ~$39,000/month            |

To estimate the proxy-related cross-AZ fraction: multiply total read/write request rate by the average payload size by `P(proxy)` by `P(cross_AZ)`. For most clusters, coordinator proxying is the dominant source of cross-AZ traffic outside of replication, particularly for read-heavy workloads where response payloads traverse the proxy path on every request.

### Bulk Write Path

The cost argument is stronger for write-heavy workloads. Bulk indexing requests are typically 5-15 MiB per request, 100-1000x larger than read responses. When a bulk request lands on a node that must proxy to a shard host, the entire multi-megabyte payload crosses the internal network (and potentially an AZ boundary). The formula is the same, but `d_avg` for bulk requests dominates the bandwidth term.

### Example: 32-Node Cluster (3 AZs)

Read workload: 50,000 req/s, average response 8 KiB, weighted eta = 0.80:

```
Proxy cross-AZ BW   = 50,000 x 8 KiB x 2 x 0.80 x 0.667 = 347 MiB/s
Monthly              = 347 MiB/s x 2,592,000s = 856 TiB
Cost at $0.02/GiB   = 856 x 1024 x $0.02 = ~$17,500/month saved
```

### Example: 256-Node Cluster (3 AZs)

Mixed workload: 200,000 read req/s (12 KiB avg) + 20,000 bulk req/s (8 MiB avg), weighted eta = 0.85:

```
Read proxy xAZ BW   = 200,000 x 12 KiB x 2 x 0.85 x 0.667 = 2.18 GiB/s
Bulk proxy xAZ BW   = 20,000 x 8 MiB x 2 x 0.85 x 0.667 = 182 GiB/s
Monthly read cost    = 2.18 x 2,592,000 x $0.02/GiB = ~$113K/month saved
Monthly bulk cost    = (dominated by replication, but proxy fraction still significant)
```

Note: bulk indexing traffic is partially coordinator-proxied and partially direct-to-primary. The savings depend on how much of the bulk path passes through a coordinator proxy under round-robin. For clusters with dedicated ingest nodes, the ingest-to-primary hop is always present regardless of routing; the savings come from routing bulk requests to ingest nodes that are co-located with primary shards.

## Scatter-Gather Reduction (Aggregations)

For aggregation queries, the coordinator scatters sub-queries to all P primary shards and merges partial results. Each shard incurs fixed overhead:

```
Messages = 2 x P                    (one scatter, one gather per shard)
Merge    = O(P x B)                 (B = aggregation bucket count)
Fixed    = P x C_fixed              (per-shard reader init, field data load, serialization)
```

Consolidating shards (increasing shard size, reducing shard count) proportionally reduces all three:

```
Reduction factor = P_before / P_after
```

### Example: Shard Consolidation on 256-Node Cluster

An over-sharded 1.5 TiB index with 200 primary shards (each approximately 7.5 GiB) consolidated to 12 primary shards (each approximately 125 GiB):

| Metric                     | Before (200P) | After (12P) | Improvement           |
| -------------------------- | ------------- | ----------- | --------------------- |
| Scatter messages           | 400           | 24          | 16.7x                 |
| Merge overhead             | O(200 x B)    | O(12 x B)   | 16.7x                 |
| Per-shard fixed cost       | 200 x C       | 12 x C      | 16.7x                 |
| Shard-hosting nodes        | ~150          | ~12         | 12.5x                 |
| Coordinator hop rate (eta) | 41%           | 95%         | 2.3x more direct hits |

The per-shard fixed costs -- segment reader initialization, field data loading, doc values memory-mapping, and partial result serialization -- are frequently the dominant cost for simple aggregations on small result sets. Reducing shard count by 16x eliminates 94% of this overhead.

Additionally, fewer shards improve Lucene-level efficiency:

- **Segment merge**: Fewer independent segment hierarchies reduce redundant merge work.
- **Global ordinals**: Terms aggregations build per-shard ordinal mappings. The coordinator merges these at O(P x unique_terms); fewer shards reduce merge work proportionally.
- **Query cache**: OpenSearch caches results at the shard level. Fewer, larger shards produce more valuable cache entries that are invalidated less frequently.

## Summary Formulas

| Metric                          | Formula                                |
| ------------------------------- | -------------------------------------- |
| Hop elimination rate            | eta = 1 - n_s / N                      |
| Availability gain               | deltaA = p^2 x eta x (1 - p)           |
| Bandwidth saved / request       | deltaBW = 2d x eta                     |
| Coordinator RAM saved / request | deltaM = d x eta                       |
| Blast radius (links at risk)    | n_s (scored) vs N(N-1)/2 (round-robin) |
| Scatter-gather reduction        | P_before / P_after                     |
| Cross-AZ cost saved             | proxy_xAZ_cost x eta                   |
| Client-side index cache         | ~200-500 B per index                   |
| Client-side connection overhead | ~120-300 B per node                    |

## Further Reading

- [Connection Scoring and Request Routing](connection_scoring.md) -- Algorithm details, configuration, and architecture
- [Request Routing](request_routing.md) -- Router types and policy chain structure
- [Connection Pool](connection_pool.md) -- Pool lifecycle and health checking
- [Cluster Health Checking](cluster_health_checking.md) -- Readiness gates and health probes

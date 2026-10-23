# Multi-Get

This guide covers `DocumentClient.MGet`: how the client fetches the documents of one mget, the two drivers that do it, and how the direct driver picks the nodes it reads from. The [`MGetDriver`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchapi#MGetDriver) doc comment has sequence diagrams of the requests each driver sends.

## The two drivers

| Driver | `OPENSEARCH_GO_MGET` | What it sends |
| --- | --- | --- |
| `MGetDriverCoordinator` (default) | `coordinator` | One request. The node that receives it groups the documents by shard, sends each shard's group to a node that holds a copy, and answers when every shard has. |
| `MGetDriverDirect` | `direct` | One request to each node it reads from, with `preference=_local`, carrying only the documents that node holds. The node reads them from its own shard copies, with no second hop, and the client puts the answers back in the order they were asked for. |

Select a driver with `MGetConfig.Driver`, `OPENSEARCH_GO_MGET`, or per call with `opensearchapi.WithMGetDriver`. `opensearchutil.MGet` always uses the direct driver. A document the direct driver cannot route to a node, such as one named through an alias or in an index whose shard map is not known yet, goes through a coordinating node in a request of its own, alongside the direct requests.

## What each driver costs

For an mget whose documents fall in `S` shards, on `K` data nodes, where each shard has `C` copies:

|                                    | Coordinator                                                                | Direct                                                                     |
| ---------------------------------- | -------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| Client requests                    | 1                                                                          | `R`, one per node for an mget under `MaxDocsPerRequest` documents per node |
| Node-to-node requests              | `S - L`, where `L` is about `S/K`, the shards the coordinator holds itself | 0                                                                          |
| Get thread pool tasks              | `S`                                                                        | `S` with fewest-nodes; more with least-loaded                              |
| Hops on the latency path           | 2                                                                          | 1                                                                          |
| Network transfers of each document | 2                                                                          | 1                                                                          |

On the server, every shard request is one task on the get thread pool of the node that reads it, and the task reads its documents one after another. A node sending a shard request to itself skips the network but not the task.

## Choosing the nodes: fan-out

The direct driver can read a shard from any of its copies. `MGetFanOut` decides which:

| Fan-out | `OPENSEARCH_GO_MGET_FANOUT` | How it picks |
| --- | --- | --- |
| `MGetFanOutFewestNodes` (default) | `fewest-nodes` | Reads the mget's shards from as few nodes as it can. Nodes the mget must reach anyway, such as a primary a realtime read of a segment-replicated index must use, take every shard they hold first. Then it repeatedly takes the node that holds the most shards not yet assigned, the least loaded on a tie. A node whose get thread pool is rejecting work is left out while another copy is on a node that is not. |
| `MGetFanOutLeastLoaded` | `least-loaded` | Reads each document from the copy on the node with the fewest documents in flight, counting the ones this mget has already placed, and from a replica on a tie. |

Select it with `MGetConfig.FanOut`, `OPENSEARCH_GO_MGET_FANOUT`, or per call with `opensearchapi.WithMGetFanOut`.

### Why fewest-nodes is the default

Least-loaded spreads a shard's documents over its copies, so a shard with two or more documents in the mget is usually read twice: two get thread pool tasks, on two nodes, for one shard. Fewest-nodes reads each shard once, as the coordinator does, and reaches at most about `K/C` nodes. The table is calculated, not measured: one mget on idle nodes with evenly placed copies, averaged over 200 random mgets.

| Nodes | Shards | Copies | Docs | Least-loaded requests | Least-loaded tasks | Fewest-nodes requests | Fewest-nodes tasks | Requests | Tasks |
| ----: | -----: | -----: | ---: | --------------------: | -----------------: | --------------------: | -----------------: | -------: | ----: |
|   300 |    300 |      2 |   10 |                    10 |                 10 |                    10 |                 10 |      -3% |   -2% |
|   300 |    300 |      2 |  100 |                    95 |                 97 |                    73 |                 85 |     -23% |  -13% |
|   300 |    300 |      2 | 1000 |                   298 |                512 |                   150 |                289 |     -50% |  -44% |
|   300 |    300 |      3 | 1000 |                   300 |                644 |                   100 |                290 |     -67% |  -55% |
|   300 |   3000 |      2 | 1000 |                   299 |                932 |                   150 |                851 |     -50% |   -9% |
|   300 |   3000 |      2 | 5000 |                   300 |               3419 |                   150 |               2432 |     -50% |  -29% |
|   300 |     30 |      2 | 1000 |                    60 |                 60 |                    30 |                 30 |     -50% |  -50% |

For the same mgets the coordinator runs as many tasks as fewest-nodes, plus one node-to-node request per shard it does not hold itself: 289 to 849 in the rows with 1000 documents, against at most 150 client requests from the direct driver. Least-loaded runs more get thread pool tasks than the coordinator does.

Fewer nodes also means fewer things that can go wrong. Every node an mget reaches is one more node that can be slow, time out, or fail it, so fewest-nodes adds documents to a node the mget already reaches before it sends a request to another node, however idle that node is.

An mget of a few documents spread over many shards reaches as many nodes either way, one per document, so the choice matters for mgets with more documents than nodes, or with several documents per shard.

### What fewest-nodes gives up

More of the mget's documents go to each node it reaches, so one mget has less parallelism. That matters most for a large mget whose documents share one shard, for example through one routing value: fewest-nodes sends all of them to one copy, cut into requests of `MaxDocsPerRequest` (500), where least-loaded would split them over the shard's copies.

The per-node window bounds what is in flight to each node, across every mget the client sends. It counts documents, starts at `MaxInFlightDocsPerNode` (8 × `MaxDocsPerRequest`, 4000 by default), halves when a node rejects requests, never falls below `MaxDocsPerRequest`, and grows back by `MaxDocsPerRequest` for each window's worth of documents the node accepts. So 5000 documents in one shard go to one node as 10 requests, 8 at a time.

Choose `MGetFanOutLeastLoaded` when single mgets are large and concentrated on few shards, and their latency matters more than the cluster's total work.

## Related

- [Environment Variables](config-envvars.md#mget) - `OPENSEARCH_GO_MGET` and `OPENSEARCH_GO_MGET_FANOUT`.
- [Document Lifecycle](indexing-document_lifecycle.md) - Getting single documents.

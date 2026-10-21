# Bulk Indexer

> **Runnable example:** [`_samples/indexing-bulk_indexer.go`](../_samples/indexing-bulk_indexer.go)

`opensearchutil.BulkIndexer` is a client for continuous ingestion. You add documents one at a time; it collects them into one buffer per shard, sends each full buffer as a bulk request to the node that hosts the shard's primary, retries what failed if you ask it to, and reports the outcome of every document through a callback.

**Which one to use.** The [Bulk guide](indexing-bulk.md) covers the `bulk` API itself: one request that you build, send and check. Use it when you already have a batch in hand. Use `BulkIndexer` when documents keep arriving and you want the batching, routing, backpressure and retries handled for you. The Bulk guide's advice on [timeouts](indexing-bulk.md#timeout-configuration) and [document IDs](indexing-bulk.md#use-client-assigned-document-ids-for-recoverability) applies here too.

## A long-lived client

A `BulkIndexer` is a long-running, stateful client, not a per-request helper. It holds a buffer for each shard, learns how fast each node and shard is, and runs goroutines and a timer. Create one when the process starts, and keep it for as long as the process writes to the cluster.

Call `Flush` at the end of each unit of work, and reuse the indexer for the next one:

```go
for job := range jobs {
	for _, doc := range job.Documents {
		if err := indexer.Add(ctx, itemFor(doc)); err != nil {
			return err
		}
	}

	// The job ends here: wait until every item added so far is final.
	if err := indexer.Flush(ctx); err != nil {
		return err
	}
	commit(job) // every callback for this job has already run
}

// Once, when the process shuts down.
return indexer.Close(ctx)
```

An indexer created for each job throws away what it learned about the nodes, so every new one starts from nothing, and one that is never closed leaves its goroutines running. Share one indexer between goroutines instead: `Add` is safe for concurrent use.

## Concepts

| Term | Meaning |
| --- | --- |
| **Item** | One action (`index`, `create`, `update`, `delete`) on one document, with its callbacks and optional `Metadata`. Exactly one of its callbacks runs, once: `OnSuccess` or `OnError`. |
| **Job** | Not a type: the items added between two calls to `Flush`. `Flush` marks where a job ends. |
| **Shard buffer** | The items bound for one shard, collected until the buffer is sent as one bulk request. |
| **Governor** | A limit that holds work back. The indexer has five, listed below. Each one that adapts is driven by a single signal. |
| **Signal** | Something the indexer observes about the cluster: a 429, how long a request took, the state of a node's write pool. A signal can drive a governor or inform a choice, such as which node to use, but on its own it holds nothing back. |
| **Node window** | The governor on how many bytes may be in flight to a node at once. A 429 halves it, and it grows back slowly while the node accepts writes. |
| **Controllers** | The logic behind the two governors that adapt. The _shard-buffer controller_ sizes a shard's buffer from the shard's write latency (a PID controller, or a fixed size). The _node-window controller_ cuts the node window on a 429 and grows it back (AIMD), or holds it fixed. Either can be turned off. See [Sizing batches](#sizing-batches). |
| **Retry policy** | Optional. Decides, for each failed item, whether to send it again and after what delay. See [Retrying failed items](#retrying-failed-items). |

The five governors, and the signal that drives each:

| Governor              | Holds back                                                               | Driven by                                                        |
| --------------------- | ------------------------------------------------------------------------ | ---------------------------------------------------------------- |
| `MaxMemoryBytes`      | `Add`, once the indexer holds this much                                  | nothing: fixed                                                   |
| One request per shard | a shard's next buffer, until its request is answered                     | nothing: fixed, to keep each shard's documents in order          |
| Shard buffer size     | how much a shard collects before it is sent, up to `FlushBytes`          | the shard's write latency, compared with its node's other shards |
| Node window           | bytes in flight to a node                                                | 429s from that node                                              |
| Node request limit    | requests in flight to a node: `NumWorkers`, or `GOMAXPROCS` when it is 0 | nothing: fixed                                                   |

The node's write thread pool, as the transport tracks it, is a signal and governs nothing. The indexer uses it to choose where documents that belong to no shard go, to start a shard's buffer small on an overloaded node, and to pause a node window's growth for a few requests when the pool starts rejecting.

The indexer is organized as follows:

```
BulkIndexer                     MaxMemoryBytes: Add waits once the indexer holds this much
├── node A                      node window (bytes, cut by 429s) and node request limit (requests)
│   ├── shard 0 buffer          buffer size (follows latency); one request in flight per shard
│   └── shard 3 buffer
├── node B
│   └── shard 1 buffer
└── documents with no shard     sent to the nearest node whose write pool has room
```

An item passes through these stages:

1. `Add` places the item in its shard's buffer. If the indexer already holds `MaxMemoryBytes`, `Add` waits until memory is freed. It also waits when its shard holds more than an even share of that limit while other shards are waiting for room (see below).
2. A buffer is sealed, to be sent as one bulk request to the node that hosts the shard's primary, when it is full (see [Basic use](#basic-use)), when `FlushInterval` elapses, or when `Flush` or `Close` is called. It is sent once the shard has no request in flight, the node is under its request limit, and the buffer fits in the node's window. Until then it waits, still counted against `MaxMemoryBytes`.
3. The response is read **item by item**. A bulk request is not all-or-nothing: a `200` can carry items the cluster rejected, which is a partial success, not an error. An item the cluster accepted gets `OnSuccess`.
4. An item that was rejected, or whose whole request failed, is classified by layer and kind (see [Retrying failed items](#retrying-failed-items)). If a retry policy asks for a retry, the item goes back into a buffer and is sent again; otherwise it gets `OnError`. [Reading an error](#reading-an-error) describes what the error holds.
5. Each completed request also feeds the controllers: its latency feeds the shard's, and a 429 feeds the node's.

When `Flush` returns, every item added before the call has had its `OnSuccess` or `OnError` called. That does not mean every item succeeded: the callbacks, or `OnUnhandledError` for an item with no `OnError`, report the ones that did not.

All shards draw on the one `MaxMemoryBytes` limit, and a shard whose requests are slow keeps its bytes until they are answered. To stop one such shard from taking the whole limit, a shard may hold no more than an even share of it while other shards are waiting for room, and the callers waiting on other shards are served first. While no other shard is waiting, a shard may use the whole limit. A shard that holds nothing may always take one item, so that no shard is left with nothing in progress; the limit can therefore be exceeded by at most one item per such shard.

### Progress guarantees

The governors delay work but cannot stop it. Whatever state the cluster is in:

- A shard's buffer is never required to be smaller than 1 byte (`MinBytes` is at least 1, including under `ShardFixed`), and it is sealed once it holds `FlushBytes`. So a buffer that holds an item has always reached the smallest size it can be asked to reach, and no buffer waits for a size it cannot reach. An item larger than the size is sent alone.
- A shard that holds nothing may always take one item, even past `MaxMemoryBytes`.
- When nothing is in flight to a node, one sealed buffer is sent whatever its size, even past the node's window.

So the memory limit and the node windows make a caller wait for requests to complete, and never leave a shard or a node with nothing to send.

## Basic use

```go
indexer, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
	Client: client,
	Index:  "events",
})
if err != nil {
	return err
}

err = indexer.Add(ctx, opensearchutil.BulkIndexerItem{
	Action:     "index",
	DocumentID: "evt-0001",
	Body:       strings.NewReader(`{"title":"Test"}`),
	OnSuccess: func(ctx context.Context, item opensearchutil.BulkIndexerItem, resp opensearchapi.BulkRespItem) {
		fmt.Printf("indexed %s\n", item.DocumentID)
	},
	OnError: func(ctx context.Context, item opensearchutil.BulkIndexerItem, err *opensearchutil.BulkError) {
		fmt.Printf("failed %s: %s %s error: %v\n", item.DocumentID, err.Layer, err.Kind, err)
	},
})
if err != nil {
	return err
}

// Send what has been added so far and keep the indexer open.
if err := indexer.Flush(ctx); err != nil {
	return err
}

// Wait until the indexer is done with every item, then release it.
if err := indexer.Close(ctx); err != nil {
	return err
}
fmt.Printf("%+v\n", indexer.Stats())
```

The indexer sends what it has buffered when a buffer is full, when `FlushInterval` (default 30 seconds) elapses, and when `Flush` or `Close` is called. When a buffer counts as full depends on the sizing mode. By default sizing is adaptive: a controller sizes each shard's buffer from how quickly the node serves that shard's requests, up to `FlushBytes` (default 5 MiB). With `Sizing.Shard` set to `ShardFixed`, a buffer is full at exactly `FlushBytes`. See [Sizing batches](#sizing-batches).

`Body` is an `io.ReadSeeker` because the indexer rewinds it. It reads the document from offset 0, wherever the reader is positioned when you pass it in: once to size it, once to fingerprint it, and once for each attempt to send it, so every attempt, retries included, sends the same bytes. If you track your own position in a reader, pass a reader over just the document, such as an `io.SectionReader`. Set `Index` on an item to send it to an index other than the configured default.

Things to be aware of:

- **`Add` after `Close` returns `ErrBulkIndexerClosed`.** Otherwise `Add` returns an error only when its context ends before the item is queued. It blocks while the indexer holds `MaxMemoryBytes`, and while its shard holds more than an even share of that limit with other shards waiting.
- **The documents of one shard keep their order.** Items with a `DocumentID` or a `Routing` value are written in the order they were added: a shard has one request in flight at a time, and a retry happens in that request's place, so a later buffer for the shard cannot overtake it. An item the indexer cannot place in a shard, because there is no shard router or the index's shard map is not known yet, is kept in order by its ID instead: its buffer is not sent until the earlier buffer holding the same ID has finished, while buffers that share no ID go out in parallel.

  The order holds when documents start going somewhere else. A shard that moves to another node keeps its order, and so does one whose node is unknown: only where its next buffer goes changes. A change in the index's shard geometry is different. When the index is recreated, split or shrunk, or the router loses the index's shard map, a document's next action can map to a different shard from its last one. When `Add` sees such a change, it sends everything already added for the index's shards, as placed, and waits for all of it to be answered before it places the new item, so nothing overtakes it. The server routes each document itself, so those items still land on the right shard, and none fails because of the change. The router reports geometry changes through `ShardTarget.Generation`; a custom `ShardRouter` that returns 0 reports none, and the indexer cannot detect them.

- **Callbacks run on the goroutine that sends the request.** A slow callback delays its own buffer and the shard's next buffer, but not the rest of the indexer. Do not call `Add`, `Flush` or `Close` from a callback; `Cancel` is safe.
- **Every item gets exactly one callback**: `OnSuccess` or `OnError`.

## Flush, Close, and Cancel

|          | Waits for                                                                     | Leaves the indexer open       | Use it to                                              |
| -------- | ----------------------------------------------------------------------------- | ----------------------------- | ------------------------------------------------------ |
| `Flush`  | the items added before the call, including retries                            | yes                           | drain at a checkpoint, or at the end of a unit of work |
| `Close`  | every item, including requests in flight and retry backoff                    | no                            | shut down cleanly                                      |
| `Cancel` | nothing: it cancels requests in flight, ends backoff, and returns immediately | no; `Close` is still required | shut down immediately                                  |

When `Flush` returns without a context error, every item it covered has already had its callback, so you can commit an external position from what the callbacks recorded. If its context ends first, `Flush` returns the context's error and stops waiting; the items still get their callback, exactly once, possibly after `Flush` has returned.

`Close` blocks until every item is final. If its context ends first, it escalates to `Cancel`: the items not yet final fail, the requests unwind, and `Close` returns the context's error. To bound the wait, pass a context with a deadline; to avoid waiting at all, call `Close` in a goroutine.

### Closing an indexer

An indexer is more than a buffer. It runs a periodic flush on a timer and goroutines that send requests, and it holds pooled buffers and, when no `Client` was supplied, a client of its own. `Close` sends whatever is still buffered and then stops and releases all of these. An indexer dropped without `Close` keeps its goroutines, timer and memory, and any item still in a buffer is lost when the process exits. `Flush` is not a substitute: it drains what has been added and leaves everything running. Call `Close` once, when the indexer is no longer needed, and reuse the indexer until then (see [A long-lived client](#a-long-lived-client)).

After `Cancel`, every item the indexer has not finished fails through its `OnError`, exactly once. An item that was never sent fails with an error of layer `LayerIndexer` and kind `KindCanceled`, which matches `opensearchutil.ErrIndexerCanceled` and `context.Canceled`. An item that was in a request when the indexer was canceled fails with that request's error, which does not tell you whether the item was written. `Add` after `Cancel` fails the item the same way and returns nil.

## Reading an error

`OnError` receives a `*opensearchutil.BulkError`, which names the layer that failed (`Layer`) and the reason (`Kind`), and carries the details:

| Event                                                                 | `Layer`        | `Kind`                                         | Details                                                             |
| --------------------------------------------------------------------- | -------------- | ---------------------------------------------- | ------------------------------------------------------------------- |
| The cluster rejected this item (version conflict, mapping error, 429) | `LayerItem`    | by status                                      | `Resp` is the item's response and `Status` its status; `Err` is nil |
| The item was never sent (cannot serialize, no ID)                     | `LayerItem`    | `KindPermanent`                                | `Err` is the reason                                                 |
| The whole request failed (network error, 5xx, 429)                    | `LayerRequest` | by status, or `KindTransport` with no response | `Err` wraps the request error, and `Status` is the request's status |
| The indexer was canceled before the item was sent                     | `LayerIndexer` | `KindCanceled`                                 | `Err` matches `ErrIndexerCanceled` and `context.Canceled`           |

`errors.Is` matches the error against the sentinel for its layer (`ErrItemLayer`, `ErrRequestLayer`, `ErrIndexerLayer`), the sentinel for its kind (`ErrRejected`, `ErrUnavailable`, `ErrTransport`, `ErrPermanent`, `ErrIndexerCanceled`), and its cause:

```go
OnError: func(ctx context.Context, item opensearchutil.BulkIndexerItem, err *opensearchutil.BulkError) {
	switch {
	case errors.Is(err, opensearchutil.ErrIndexerCanceled):
		// Shutting down; the item was never sent.
	case errors.Is(err, opensearchutil.ErrTransport):
		// The outcome is unknown: the item may or may not have been written.
	default:
		log.Printf("%s %s: %v", err.Layer, err.Kind, err)
	}
},
```

A request error is ambiguous: the request may have reached the cluster, in part or in full, before it failed. The indexer reports the same error to every item in the request. An item with no `OnError` is reported to `BulkIndexerConfig.OnUnhandledError` instead. An explicit `Flush` reports through its return value and does not call `OnUnhandledError`.

The transport retries on its own as well, underneath the indexer. It resends a request that failed with a network error or got a status in its `RetryOnStatus` (502, 503 and 504 by default). When the client has a router that tracks the node's write thread pool, as the default router does, it also resends a request the node answered with a 429, and routes the resend afresh, so it can reach a different node. See [Retry and Backoff](transport-retry_backoff.md). The indexer sees an error only once the transport's retries are used up, so a 429 that the transport resent successfully does not cut the node window.

## Retrying failed items

Set `BulkIndexerConfig.RetryPolicy` to have the indexer resend failed items. The indexer runs the retry loop; the policy decides the delay and when to stop:

```go
retry := opensearchutil.RetryPolicyFunc(func(attempt int, err *opensearchutil.BulkError) (time.Duration, bool) {
	if errors.Is(err, opensearchutil.ErrPermanent) || attempt >= 5 {
		return 0, false
	}
	return 100 * time.Millisecond << (attempt - 1), true // 100ms, 200ms, 400ms, ...
})
```

`attempt` counts the requests that have carried the work so far; it is 1 after the first request fails. The policy returns the delay and whether to retry. The indexer sets no limit of its own, so a policy that always returns true retries a persistent failure forever.

### Layer and kind

The policy is asked about two layers of error, told apart by `err.Layer`:

- **`LayerRequest`**: the whole request failed. `err.Err` is the request's error. One call covers every item in the request.
- **`LayerItem`**: the cluster answered and rejected one item. `err.Resp` is that item's response. There is one call per rejected item.

`LayerIndexer` errors, from a canceled indexer, are never retried and never reach the policy. The indexer classifies each error by kind:

| Kind              | Sentinel             | Meaning                                                                                                              | Safe to retry               |
| ----------------- | -------------------- | -------------------------------------------------------------------------------------------------------------------- | --------------------------- |
| `KindRejected`    | `ErrRejected`        | a write queue was full (HTTP 429)                                                                                    | yes                         |
| `KindUnavailable` | `ErrUnavailable`     | the cluster or a proxy could not serve the request (HTTP 503)                                                        | yes                         |
| `KindTransport`   | `ErrTransport`       | the outcome is unknown: no response, a server error other than 503, or an unreadable response                        | only with deterministic IDs |
| `KindPermanent`   | `ErrPermanent`       | the same request would fail the same way: a mapping error, a version conflict, any other 4xx, an unpairable response | no                          |
| `KindCanceled`    | `ErrIndexerCanceled` | the indexer was canceled before the work was sent                                                                    | never offered to the policy |

### Callbacks during retries

While an item is being retried, neither `OnSuccess` nor `OnError` runs for it. One of them runs once, when the indexer is done with the item: the item was accepted, the policy declined to retry it, or its retries were abandoned. A rejected item is resent in one request with the other items being retried, after the longest delay any of them was given.

Set `BulkIndexerConfig.Timeout`, the server-side timeout, shorter than the context you pass to `Flush` and `Close` whenever a `RetryPolicy` is set. The server-side timeout bounds how long each action waits on the cluster, for example for a shard whose primary is not active. With it shorter, an action stuck waiting comes back as a failed item (`KindUnavailable`) that the policy can retry precisely. Without it, the context can end first, the whole request fails with an unknown outcome, and the server goes on with it while the retry sends the same documents again. See [Bulk: Timeout configuration](indexing-bulk.md#timeout-configuration).

The wait before a retry watches both a timer and the context. It ends early, and the waiting items fail with the error they last got, when the flush's context ends: the context passed to `Flush` or `Close`, or a call to `Cancel`. The buffer keeps its shard's request slot while it waits, so the shard's next buffer is held back and its memory stays counted. Keep delays short relative to `FlushInterval`.

## Document IDs

Retrying a `KindTransport` error can write a document twice, because the first attempt may have succeeded. With a deterministic `_id` the second write replaces the first; with a server-generated ID it creates a duplicate. So when a `RetryPolicy` is set, every item must have a `DocumentID`. An item without one is not sent, and fails through its own `OnError` with an error that matches `opensearchutil.ErrMissingDocumentID`. `Add` still returns nil, and that `OnError` runs on the goroutine that called `Add`. Without a policy nothing changes, and the cluster generates the ID.

For documents with no natural ID, set `BulkIndexerConfig.Fingerprinter` to derive one:

```go
fingerprint := func(ctx context.Context, item opensearchutil.BulkIndexerItem) (string, error) {
	body, err := io.ReadAll(item.Body)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:8]), nil
}
```

`Add` calls the fingerprinter only for an item whose `DocumentID` is empty, and before the item is placed in a shard, so a generated ID routes exactly as an explicit one does and per-ID ordering holds. The fingerprinter must be deterministic and safe for concurrent use. It may read `Body`; the indexer rewinds it afterward. An error, or an empty ID, fails the item through its `OnError`.

## Carrying data with Metadata

`BulkIndexerItem.Metadata` is an opaque `any` that comes back unchanged in the item passed to `OnSuccess` and `OnError`. It is never sent to the cluster. Use it to carry what a callback needs, such as the document's stream offset, instead of allocating a closure for each item:

```go
type source struct {
	partition string
	offset    int64
}

item := opensearchutil.BulkIndexerItem{
	Action:   "index",
	Body:     body,
	Metadata: source{partition: "events/0", offset: 4102},
	OnSuccess: func(_ context.Context, item opensearchutil.BulkIndexerItem, _ opensearchapi.BulkRespItem) {
		src := item.Metadata.(source)
		// ...
	},
}
```

## Committing a stream position

When you index from a stream such as Kafka, the consumer may commit its position past a record only once every earlier record is done. The indexer completes records out of order, because different shards finish at different times. A client library that commits the highest completed record would commit past a gap and lose the records in it.

`opensearchutil.FrontierTracker` reports each stream's frontier: the lowest offset that is not yet complete. Every offset below it is complete, so the frontier never passes a gap. Register where each stream starts, mark each record complete in `OnSuccess`, and read the frontier to commit:

```go
var tracker opensearchutil.FrontierTracker[string]
tracker.Begin("events/0", 100) // the first offset read

// In OnSuccess:
tracker.Complete(src.partition, src.offset)

// At each commit:
frontier, _ := tracker.Frontier("events/0") // every offset below frontier is done
```

The tracker has no failed state, by design. A failed record must not move the frontier, so you do not call `Complete` for it: the frontier stays below that record until the application deals with it, and the completions after it are held in memory until then.

Before releasing a stream, for example when a partition is revoked in a rebalance, do these steps in order:

1. `Flush` the indexer, so that every record passed to it has completed or failed.
2. Call `tracker.Forget` and take the frontier it returns. Completions that arrive afterward are ignored.
3. Commit that frontier, and only that frontier.

The order matters: the flush comes first, and the frontier is read after it. Once `Flush` has returned and no more records are added for the stream, nothing more can complete, so `tracker.Frontier` and `tracker.Forget` return the same value. Reading the frontier with `Frontier`, committing, and then calling `Forget` is just as safe, and keeps the stream tracked if the commit fails.

## Sizing batches

By default the indexer does not send every buffer at `FlushBytes`. `FlushBytes` is the most a shard's buffer collects, and two controllers work beneath it. Each sets one governor from one signal. `BulkIndexerConfig.Sizing` tunes them; its zero value selects defaults derived from `FlushBytes`.

|                                                         | Governs                                                    | Signal                                                                              | Response                                                                 |
| ------------------------------------------------------- | ---------------------------------------------------------- | ----------------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| **Shard-buffer controller** (PID on latency, or fixed)  | how many bytes a shard's buffer collects before it is sent | how fast the node served that shard's recent requests, relative to its other shards | a shard slower than the rest of its node: smaller buffers for that shard |
| **Node-window controller** (responds to 429s, or fixed) | how many bytes may be in flight to a node at once          | whether the node answered a request with a 429                                      | an overloaded node: less in flight, then a gradual increase              |

### Why it is adaptive

A single indexer cannot know how much a cluster can take. The cluster is shared by many writers, its capacity changes as other work starts and stops, and a batch size that suits a node at one moment overloads it at another. A fleet of fixed-size writers either leaves capacity unused or fills the write queues and gets 429s. So the indexer measures instead, using two signals that need no coordination between clients:

- **429 responses** say that a node is overloaded: its write queue is full. When a node answers with one, the indexer cuts the bytes it keeps in flight to that node, then raises them in small steps while the node keeps accepting writes. This is additive increase, multiplicative decrease (AIMD), the scheme TCP uses to find a network's capacity.
- **Write latency** tells shards apart. A PID controller compares how quickly the node serves each shard's requests with how quickly it serves its other shards, and gives a shard that is slower than the rest smaller buffers, so a hot or expensive shard does not take the node's time from the others. A slowdown the whole node shares is just the node being busy: no shard is cut for it, and the node keeps getting as much as it accepts until it answers with a 429.

Together they keep a node busy without pushing it into rejections. Because every client in the fleet makes the same adjustments from the same signals, their combined load stays near what the cluster can take.

The two controllers matter to different workloads. The node window matters to any workload that can saturate a node: it turns a 429 into less load rather than a burst of retries. The shard-buffer controller compares a shard only with the other shards on its node, so it matters only when shards differ. A hot shard, a shard whose documents are expensive to index (large, many fields, heavy ingest pipelines), or a shard on a node that is also merging or recovering, is slower than its neighbors, and full-size requests for it take the node's time from the other shards. The controller gives that shard smaller buffers, so it gets its share of the node's time and no more. When every shard on a node is about as fast as the others, which is typical of one index with evenly distributed IDs, nothing is cut: every buffer stays at `FlushBytes`, and adaptive sizing behaves like `ShardFixed`, except that a shard that starts on an overloaded node starts at `InitialBytes` and climbs from there.

#### The shard-buffer controller

The controller's input is the time each request spent on the node: the round trip minus the network distance. A request carries one shard, so this measures that shard's own speed. The controller compares it with what the node, at its best recent performance, would need for a request of the same size: a fixed cost per request, plus the bytes at the best service rate seen recently. The fixed cost is learned from small requests, where it is a visible part of the time. Without it, a small request would be judged against the throughput of large ones, which it cannot reach even on a healthy node. The comparison is smoothed over the last several flushes, because a single flush's time is mostly noise: queueing, garbage collection, merges. The smoothed level is then compared with the mean level of the node's other shards. A shard alone on its node, such as the buffer for documents that belong to no shard, is compared with itself and keeps its size.

- A shard that starts on a node that is not overloaded starts at `FlushBytes`, so a node that can take full requests gets them from the first one. A shard that starts on an overloaded node, one whose write pool is rejecting or whose node window a 429 cut and has not grown back, starts at `InitialBytes` (`FlushBytes/4` by default) and climbs to full size as the node keeps up. Setting `InitialBytes` makes every shard start there. To always send `FlushBytes`, with neither the climb nor any shrinking, set `Sizing.Shard` to `ShardFixed`, or `OPENSEARCH_GO_BULK_SHARD=fixed`.
- While the smoothed time stays within `Headroom` of the expected time, the buffer grows by `StepBytes` per flush.
- When the shard is slower than the rest of its node by more than that, its buffer shrinks in proportion to how far past `Headroom` it is, so that it yields to the node's other shards.
- `Headroom` widens with the signal's noise. A node whose times vary must slow by more than its usual variation before the slowdown counts; a steady node is held to `Headroom` alone.
- The recorded best rate decays slowly while requests are large, and faster while they are small. A node that is slow even for small requests was not slowed by request size, so the buffer can recover on a node whose performance has changed.

The controller learns from a request only if the node indexed at least one of its items. It ignores the time of every other request, because none of them measures how fast the node indexes: a 429, which a node answers at once because it did no work; a request that failed without a response, or with a 5xx or a 4xx that says nothing about this shard; and a request in which every item failed, such as one for a shard whose primary is not active, which fails only after the bulk `timeout` elapses. Reading any of these as a slow or fast flush would shrink or grow the buffer for a reason the buffer size cannot fix. Items refused on their own merits, such as a version conflict or a mapping error, do not stop a request from counting, as long as some other item was indexed.

One failure does change a buffer. A **413**, a request refused for being larger than the cluster accepts (`http.max_content_length`), reflects the cluster's settings rather than its state. From it the indexer learns that no later request may be larger than half the refused size, and it never raises that limit again, so it stops sending requests that cannot be accepted and does not grow its buffers back to the refused size.

The size of each step comes from a PID controller on the shard's normalized slowdown, with gains `Kp`, `Ki` and `Kd` (defaults `1`, `0.1` and `0.25`). [How the PID controller works](#how-the-pid-controller-works) explains it.

#### The node-window controller

The node window is a number of bytes: the total size of the requests the indexer has sent to a node that the node has not answered yet. A sealed buffer that would take the node past its window waits until requests complete and it fits. When nothing is in flight to the node, one buffer is sent whatever its size, so a node is never starved. A waiting buffer still counts against `MaxMemoryBytes`, which is what eventually makes `Add` wait.

The node window limits bytes, not requests. The number of requests in flight to a node is a separate governor, the node request limit: `NumWorkers`, or `GOMAXPROCS` when `NumWorkers` is 0. `GOMAXPROCS` follows a container's CPU limit, where the machine's CPU count does not, provided the program's `go.mod` declares Go 1.25 or later (older programs can set `GODEBUG=containermaxprocs=1`).

The transport also tracks a window for each node's write thread pool. That one is a signal, not a governor. The transport uses it to rank nodes when it routes a request, and the indexer uses it the same way, to choose the node for documents that belong to no shard. It would make a poor governor. It changes only when the transport polls node statistics, seconds apart. It never exceeds the pool's thread count, so it would leave the node's queue empty between requests. And it shrinks whenever every thread is busy with work queued, which is exactly how a node looks when it is taking bulk requests as fast as it can. Busy is not overloaded. A 429 is what says a node is overloaded, and a 429 is what cuts the node window.

The node window's only input is a 429: a response in which the node rejected the request, or any item in it.

- **The window starts fully open**, at `MaxMemoryBytes`, or at `NodeBytes` if set.
- **A 429 halves the bytes in flight.** The window becomes `Backoff` (one half) times the smaller of the window and the bytes that were in flight to the node when the response arrived, the rejected request's included. Cutting from the bytes in flight rather than from the window makes the first 429 count: a window that starts at the whole memory limit is far above what is ever in flight, and halving it would change nothing. Rejections of requests sent before the cut belong to the same event and do not cut the window again. The window never falls below one full buffer.
- **The window grows back slowly.** It grows by one `FlushBytes` for each window's worth of bytes the node answers, after a hold of a few responses. A window that was cut recovers over many requests; one that was never cut barely changes.
- **A node whose write pool starts rejecting pauses the window's growth** for the same few responses, without cutting it. The rejections may be other clients' requests, so this is how one client hears that a node is overloaded before it gets a 429 of its own. The transport goes on reporting the pool as rejecting until a statistics poll, seconds apart, finds no new rejections, so the indexer takes only the start of that report as an event. After the pause the window probes again, and the node's 429s govern it.
- **The window does not remember where the last 429 happened.** A 429 can come from other writers to the cluster, or from a cluster that has since gained capacity, so the window keeps probing and settles into a sawtooth around what the node currently accepts.
- **A request that fails with no response is not a 429**, and does not cut the window.

The result: a node that slows down without rejecting keeps its shards' buffer sizes and its window; a shard slower than the rest of its node gets smaller buffers; and a node that rejects has fewer bytes in flight, however fast it is.

#### Starting a shard

A shard that starts on a node, such as the first document for a new index, inherits the best service rate the node has shown; the node keeps that rate for all of its shards. Only the rate carries over: the shard learns its own fixed cost per request. Its first flushes are then compared with the expected time for a request of that size, not with its own first flush, so a shard that starts while the node is busy does not take the busy rate as its best. The first shard on a node to send a request has no such history and learns its own rate.

### How the PID controller works

A PID controller is the mechanism behind a car's cruise control. Given a target speed, it measures the actual speed, takes the difference (the _error_), and sets the throttle. It does this with three terms, each scaled by a _gain_:

| Term             | Gain | Responds to                          | Effect                                                                                                                                    |
| ---------------- | ---- | ------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------- |
| **P**roportional | `Kp` | the current error                    | The correction is proportional to the size of the error.                                                                                  |
| **I**ntegral     | `Ki` | the error summed over recent flushes | A small, persistent error accumulates and adds to each correction, so the controller does not settle at a steady offset.                  |
| **D**erivative   | `Kd` | how fast the error is changing       | A shard that suddenly falls behind is cut further before the proportional term responds; a shard that is recovering is given room sooner. |

The three results are summed into one number, `u`, which sets the step.

In the indexer, the quantity being controlled is the size of one shard's buffer, and the measurement is the shard's _level_: the time the node spent on its requests, relative to what the shard at its best recent performance would need for a request of that size, smoothed over the last several flushes and compared with the level of the node's other shards. A shard alone on its node is compared with itself. The error `e` is +1 for a shard as fast as the rest of its node, 0 when it is slower by `Headroom` (25% by default, widened by the signal's noise, called the slack below), and -1 when it is slower by twice `Headroom` plus the slack, or more. The slack is added once, so the slowdown that reaches -1 is `2 × Headroom + slack`:

```
 e
+1 ●━━━━━━━╲                       shard as fast as the rest of its node
           ╲
 0 ┈┈┈┈┈┈┈┈┈┈●┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈  slowdown = Headroom + slack: hold the size
                ╲
-1               ●━━━━━━━━━━━━━━━  slowdown of 2 × Headroom + slack or worse (clamped)
   └──────┬───────┬───────┬──────── slowdown
          0      0.25    0.5       (0 = as fast as the rest of the node, 1 = twice as slow; shown for slack = 0)
```

The step is `u = Kp·e + Ki·(sum of recent e) + Kd·(e - previous e)`. A positive `u` grows the buffer by `StepBytes` scaled by `min(u, 1)`. A negative `u` multiplies it by `1 + max(u, -0.5)`, so a single flush never takes away more than half. Over time the buffer grows while the shard keeps up with the rest of its node, and shrinks as it falls behind:

```
buffer size
FlushBytes ┤            ●━━━━━━━●                      ceiling
           │          ╱         ╲
           │        ╱            ╲        ╱╲          shard falls behind past Headroom:
           │      ╱               ╲      ╱  ╲╱━━━━     steps down in proportion,
           │    ╱                  ●╲  ╱               then settles where it
           │  ╱                       ●                keeps up
 start     ┤●
           └─────────────────────────────────────── flushes
```

(The second graph shows the shape; it is not a measurement.)

The defaults suit most clusters. If you change them, change one at a time:

| Goal                                  | Change                           | Risk                                         |
| ------------------------------------- | -------------------------------- | -------------------------------------------- |
| Faster growth and larger cuts         | raise `Kp`                       | oscillation: the size swinging up and down   |
| Less reaction to a single noisy flush | lower `Kp`, or set `Kd` to `0`   | slower response to a real slowdown           |
| No memory of past flushes             | set `Ki` to `0`                  | a mild, persistent slowdown left uncorrected |
| No adaptive shard sizing              | `OPENSEARCH_GO_BULK_SHARD=fixed` | a hot shard keeps sending full-size requests |

A gain of `0` turns that term off. The environment overrides are listed in [Environment variable overrides](#environment-variable-overrides).

### Turning a controller off

The two controllers are independent, and either can be turned off on its own:

| Setting                                       | Values                                                                                                                                                                                                                        | Default    |
| --------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------- |
| `Sizing.Shard`, or `OPENSEARCH_GO_BULK_SHARD` | `ShardPID` (`pid`): a shard's buffer follows how quickly the node serves its requests. `ShardFixed` (`fixed`): every buffer is `FlushBytes`, and the PID gains have no effect.                                                | `ShardPID` |
| `Sizing.Node`, or `OPENSEARCH_GO_BULK_NODE`   | `NodeAIMD` (`aimd`): a node's window is cut when the node answers with a 429, and grows back slowly. `NodeFixed` (`fixed`): the window stays where it started, at `NodeBytes` or `MaxMemoryBytes`, and a 429 does not cut it. | `NodeAIMD` |

The zero values, `ShardUnset` and `NodeUnset`, select the defaults. With both turned off, every buffer is sent at `FlushBytes` and the node window never changes. Either way, the governors that do not adapt still apply: no more than `NumWorkers` requests are in flight to a node, as described under [The node-window controller](#the-node-window-controller).

A gain set to `0` through its environment variable turns that term off. In code, a `Sizing.PID` left at its zero value selects the default gains, so all three cannot be set to `0` in code; through the environment they can, and then no step changes the size. To keep shard buffers at a constant size, use `ShardFixed` instead.

For example, to send every buffer at `FlushBytes` while still backing off from a node that answers with 429s:

```go
indexer, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
	Client:     client,
	FlushBytes: 5 << 20, // 5 MiB: every request a shard's buffer sends
	Sizing:     opensearchutil.BulkSizing{Shard: opensearchutil.ShardFixed},
})
```

### Why two different controllers

The controllers answer different kinds of signal. A shard buffer's signal is a measurement, the time each flush took, and it varies by degree: a shard can be slightly or much slower than its neighbors, briefly or for a long time. A PID controller uses all of that: a small slowdown brings a small cut and a large one a large cut (`Kp`), a persistent slowdown a larger cut on each flush (`Ki`), and a sudden one is met before it builds up (`Kd`).

A node window's signal is binary: the node answered with a 429, or it did not. With no magnitude to scale a step by, the window uses AIMD: it rises in small steps while the node accepts writes and falls sharply when the node refuses one, the scheme TCP uses to find a network's capacity.

The transport keeps AIMD windows of its own, one per node and thread pool, for connection scoring, described in [Request Routing](transport-routing.md#aimd-congestion-windows). Those windows share the name and the shape, and nothing else. They are signals: they rank the nodes a request may go to, and the indexer reads the write pool's to choose a node for documents that belong to no shard. The node window here is a governor: it limits the bytes in flight to a node.

### Environment variable overrides

Each of these settings can also be set from the environment, so an operator can change how a deployment sizes its batches without rebuilding it. `NewBulkIndexer` reads the variables once. A variable set to a valid value overrides the code; a variable that is unset, empty or invalid leaves the code's setting in place.

| Variable                                  | Overrides                     | Values                                     |
| ----------------------------------------- | ----------------------------- | ------------------------------------------ |
| `OPENSEARCH_GO_BULK_SHARD`                | `Sizing.Shard`                | `pid`, `fixed`                             |
| `OPENSEARCH_GO_BULK_NODE`                 | `Sizing.Node`                 | `aimd`, `fixed`                            |
| `OPENSEARCH_GO_BULK_PID_KP`, `_KI`, `_KD` | `Sizing.PID.Kp`, `.Ki`, `.Kd` | non-negative number; `0` disables the term |

The three gains tune the shard-buffer PID controller, and have no effect when `OPENSEARCH_GO_BULK_SHARD=fixed`. After each flush the controller computes `e`, the shard's distance from `Headroom` (+1 as fast as the rest of its node, 0 at `Headroom`, -1 at twice `Headroom` or slower), and takes a step `u = Kp·e + Ki·(sum of recent e) + Kd·(change in e)`. A positive step grows the buffer by up to `StepBytes`; a negative one shrinks it, by at most half in one flush.

- **`Kp`, the proportional gain (default `1`)**: sets the part of each step that is proportional to `e`, the shard's distance from `Headroom` on this flush. Doubling `Kp` doubles that part: the buffer grows faster while the shard keeps up, and shrinks further once it falls behind. Halving it makes both changes smaller.
- **`Ki`, the integral gain (default `0.1`)**: adds to each step in proportion to the sum of `e` over recent flushes, capped at ±2. While the shard stays on one side of `Headroom`, the sum adds to every step, so a persistent slowdown shrinks the buffer further on each flush, and a sustained run of fast flushes grows it faster. At `0` it adds nothing.
- **`Kd`, the derivative gain (default `0.25`)**: adds to each step in proportion to the change in `e` since the previous flush. A sudden slowdown adds a cut on the flush where it appears, before the other two terms respond, and a recovery adds growth. Noise between flushes also changes `e`, so a large `Kd` makes the size jitter on a noisy node; if it does, lower `Kd` or set it to `0`.

Common configurations:

```sh
# Send every buffer at FlushBytes. Nodes still back off on 429s.
export OPENSEARCH_GO_BULK_SHARD=fixed

# Keep a node's window where it started, whatever the node answers.
export OPENSEARCH_GO_BULK_NODE=fixed

# Stop the buffer size jittering on a noisy node: drop the derivative term.
export OPENSEARCH_GO_BULK_PID_KD=0
```

The [environment variable reference](config-envvars.md#bulk-indexer) lists every value.

## Steering a consumer by the indexer's load

A service that reads from a stream such as Kafka and writes to OpenSearch should read less when OpenSearch is slow. Reading too much and then crashing makes recovery longer, because every record read and not yet committed is replayed. `Add` alone is a poor signal for this: it blocks only once the whole of `MaxMemoryBytes` is in use, and a blocked `Add` stalls the poll loop, which can exceed the stream's poll timeout and trigger a rebalance.

`GetStats` gives better signals. It loads the indexer's statistics into a struct you own and reuse, so it allocates nothing and cannot change the indexer, and it takes no lock, so you can call it as often as you like while other goroutines call `Add`:

```go
var stats opensearchutil.BulkIndexerStats // reused: GetStats replaces everything in it
if err := indexer.GetStats(&stats); err != nil {
	return err
}

fill := stats.BudgetFill()          // 0 to 1: how much of MaxMemoryBytes is held
typical := stats.Slowdown.Median    // how much longer than expected the typical node takes
pushedBack := stats.NumRejections   // only grows: compare with the last reading
```

| Reading         | Changes when                                                                                     | Use it to                                                                                            |
| --------------- | ------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------- |
| `BudgetFill()`  | the indexer holds more than the cluster is taking                                                | slow down gradually, well before `Add` blocks                                                        |
| `NumRejections` | a node answers with a 429                                                                        | back off at once: any increase since the last reading means the cluster pushed back, however briefly |
| `NodeWindow`    | a node answers with a 429 (falls) or accepts writes again (rises)                                | tell an overloaded cluster from a recovering one: 1 means no node window is cut                      |
| `Slowdown`      | a node takes longer for its requests than it needs                                               | see slowness directly, before any node rejects: 0 is as expected, 1 is twice as long                 |
| `BufferSize`    | the indexer finds a node's shards slower than the rest of the node (falls) or keeping up (rises) | see how far the indexer has already cut its own batches                                              |

`NodeWindow`, `Slowdown` and `BufferSize` are each a `JobNodeStats`: the reading across the nodes the indexer writes to, as `Min`, `Median`, `Max` and `Mean`. A single number would mislead. The extreme alone lets one node stand for the whole cluster, and the average alone hides that node. Steer by `Median`, or by `Mean`, which weights each node by the bytes the indexer holds for it, and use `Min` or `Max` to spot an outlier. In a cluster that is slow overall, the median is near the worst node. A single slow node moves `Min` or `Max` and leaves the median alone; if that node also carries much of the work, the mean moves with it.

The consumer must also bound its own backlog: the records it has read and cannot commit yet. With a [`FrontierTracker`](#committing-a-stream-position), the backlog is the offset read minus `Frontier`, which is exactly what a crash would replay. A simple consumer keeps the backlog under a limit, pauses its partitions when `BudgetFill` passes a high threshold or `NumRejections` rises, shrinks its read buffer as `Slowdown.Median` or `Slowdown.Mean` grows, and resumes below a low threshold. The thresholds depend on the application; a wider gap between them pauses less often.

These readings follow the cluster and do not change what the indexer does; it goes on adapting to the same signals by itself. A consumer that adapts to them forms a second control loop over the same resource, so give it a longer time constant than the indexer's, or the two loops will oscillate against each other. Each value is smoothed over a node's last few requests.

## Observing the indexer

`BulkIndexerStats` counts what the indexer has done, including `NumRetries`, indexed by `ErrorKind`. For a stream of events instead, set `BulkIndexerConfig.Observer`. It follows the same pattern as the [transport's observer](transport-observer_metrics.md): embed `opensearchutil.BaseBulkIndexerObserver` and override only the methods you need.

| Method           | Called                                                                                 |
| ---------------- | -------------------------------------------------------------------------------------- |
| `OnRequestEnd`   | after each bulk request returns, including a retry; carries status, duration, and size |
| `OnRequestError` | when a whole request fails, once per attempt; the error's `Layer` is `LayerRequest`    |
| `OnItemError`    | when one item fails, once per attempt; the error's `Layer` is `LayerItem`              |
| `OnItemDone`     | once per item, when the indexer is done with it; carries the size of its document      |

`OnRequestEnd` reports the size of the whole request body, which is the buffer the indexer built: the action lines and documents of one shard, before the transport compresses it. `OnItemDone` reports one document: `BulkItemEvent.Bytes` is the length of the item's `Body`, without its action line, and is the same whether the item succeeded, was rejected or was refused. It picks out an outlier that the request average hides. The compressed size on the wire comes from the transport, not the indexer: the transport observer's `OnRequestStart` event carries the body size before compression, and `OnRequestResponse` the `Content-Length` after it.

The error events carry the `*BulkError`, with its layer and kind, the attempt, and whether the retry policy is retrying. The methods run on the goroutine that sends the request and receive the flush's context. Keep them fast and do not call back into the indexer; a tracing observer can use the context to link to the flush's span.

The [`osprom`](https://github.com/opensearch-project/opensearch-go/tree/main/osprom) and [`osotel`](https://github.com/opensearch-project/opensearch-go/tree/main/osotel) modules provide ready-made observers that record bulk requests, failures by layer and kind, retries, and final item outcomes. They are independent of the transport's `Registry`:

```go
bulkObserver := osprom.NewBulkIndexerObserver()
if err := bulkObserver.Register(promRegistry); err != nil {
	return err
}

indexer, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
	Client:   client,
	Observer: bulkObserver,
})
```

### Metrics from GetStats

`GetStats` provides the inputs for RED (rate, errors, duration) and USE (utilization, saturation, errors) metrics without an observer, and suits a periodic gauge callback. Each field of `BulkIndexerStats` is documented as a COUNTER, which only grows, or a GAUGE, which reports the current value. Derive a rate from the difference between two readings of a counter, and a mean by dividing one counter by another.

| Metric                            | Fields                                                                                        |
| --------------------------------- | --------------------------------------------------------------------------------------------- |
| Item and request rate             | `NumFlushed`, `NumFailed`, `NumRequests`                                                      |
| Batches by flush trigger          | `NumByteFlushes`, `NumTimerFlushes`, `NumPressureFlushes`, `NumCloseFlushes`                  |
| Errors by layer and kind          | `Errors[layer][kind]`, for example `Errors[LayerRequest][KindRejected]`                       |
| Mean and maximum item latency     | `ItemLatencyNanos / (NumFlushed + NumFailed)`, `MaxItemLatencyNanos`                          |
| Mean and maximum request duration | `RequestDurationNanos / NumRequests`, `MaxRequestDurationNanos`                               |
| Mean and maximum document size    | `ItemBytes / NumAdded`, `MaxItemBytes`                                                        |
| Mean request size                 | `RequestBytes / NumRequests`                                                                  |
| Utilization                       | `InFlightRequests`, `InFlightBytes`, `BufferedBytes`, `BudgetFill()`                          |
| Saturation                        | `QueuedBuffers`, `BlockedShards`, `RetryBackoffItems`, `NumBlockedAdds`, `BlockedAddDuration` |

Item latency runs from `Add` to the item's outcome, across every attempt. It includes the time spent in a buffer and in the queue, so it exceeds request duration; the difference is queueing delay. `BlockedShards` counts the shards that have a request in flight and more documents waiting behind it. `RetryBackoffItems` counts the items waiting out a retry policy's delay; their buffer keeps its shard's request slot while they wait. `GetStats` loads each value separately, so the values are not a snapshot of one instant.

`GetNodeStats` reports the same readings per node: requests and bytes in flight, queued buffers, held bytes, rejections, node window, buffer size and slowdown. Use it to find the node behind an outlier in `NodeWindow`, `BufferSize` or `Slowdown`. Pass back the slice it returned to avoid allocating:

```go
var nodes []opensearchutil.NodeStats
nodes = indexer.GetNodeStats(nodes)
for _, n := range nodes {
	slowdown.WithLabelValues(n.Name).Set(n.Slowdown)
}
```

Like `GetStats`, `GetNodeStats` takes no lock, and one node's values are not guaranteed to come from the same instant.

## See also

- [Bulk](indexing-bulk.md) - the `bulk` API this indexer builds on: request format, error handling, timeouts and routing.
- [Error Handling and Partial Failures](usage-error_handling.md) - how partial failures surface from the typed API.
- [Retry and Backoff](transport-retry_backoff.md) - the transport's own retries, beneath the indexer's.
- [Environment Variables](config-envvars.md#bulk-indexer) - the `OPENSEARCH_GO_BULK_*` overrides for batch sizing.
- [Observer-Based Metrics](transport-observer_metrics.md) - the transport observer model and the `osprom` and `osotel` modules.

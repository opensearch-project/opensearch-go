# Bulk Indexer

> **Runnable example:** [`_samples/indexing-bulk_indexer.go`](../_samples/indexing-bulk_indexer.go)

The [Bulk guide](indexing-bulk.md) covers a single `bulk` request that the caller builds and checks. `opensearchutil.BulkIndexer` builds on it for continuous ingestion. It accepts documents one at a time, keeps a buffer for each shard, sends each buffer to the node that hosts the shard, and reports the outcome of each document through callbacks.

## A long-lived client

A `BulkIndexer` is a long-running, stateful client, not a per-request helper. It holds a buffer for each shard, learns the speed and load of each node, and runs worker goroutines and timers. Create one when the process starts and keep it for as long as the process writes to the cluster.

Use `Flush` to mark the end of each unit of work, and reuse the indexer for the next one:

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

An indexer created per job discards what its controllers learned about the nodes, so each new indexer starts without that knowledge, and an indexer that is not closed leaves its goroutines running. Share one indexer between goroutines instead; `Add` is safe for concurrent use.

## Concepts

| Term | Meaning |
| --- | --- |
| **Item** | One action (`index`, `create`, `update`, `delete`) on one document, with its callbacks and optional `Metadata`. Exactly one of its callbacks runs, once: `OnSuccess` or `OnError`. |
| **Job** | Not a type: the items added between two calls to `Flush`. `Flush` marks where a job ends. |
| **Shard buffer** | The items bound for one shard, collected until the buffer is sent as one bulk request. |
| **Node window** | The number of bytes of requests the indexer may have sent to a node and not yet had answered. A buffer that would exceed it waits, sealed, until requests complete. The window is how the indexer responds to a node that rejects writes; a 429 halves it. |
| **Controllers** | The logic that sets the two sizes above. The _shard-buffer controller_ decides when a shard's buffer is full, from write latency (a PID controller, or a fixed size). The _node-window controller_ shrinks the node window when the node answers with a 429 and grows it back gradually while the node accepts writes (AIMD), or holds it fixed. Each can be disabled independently. See [Sizing batches](#sizing-batches). |
| **Retry policy** | Optional. Decides, for each failed item, whether to send it again and after what delay. See [Retrying failed items](#retrying-failed-items). |

The indexer is organized as follows:

```
BulkIndexer
├── node A                  node window: bytes out to the node at once      (shrinks on the node's 429s)
│   ├── shard 0 buffer      sent when full; one request in flight per shard (adaptive on latency, or fixed)
│   └── shard 3 buffer
├── node B
│   └── shard 1 buffer
└── unplaced                items with no known shard, sent to the nearest node with room
```

An item passes through these stages:

1. `Add` places the item in the buffer for its shard. If the indexer already holds `MaxMemoryBytes`, `Add` waits until memory is released. `Add` also waits when its shard holds more than an even share of the limit while other shards are waiting for room (see below).
2. A buffer is sent as one bulk request, to the node that hosts the shard's primary, when it is full (see [Basic use](#basic-use)), when `FlushInterval` elapses, or when `Flush` or `Close` is called. If sending it would put more bytes in flight to that node than its window allows, it waits, still counted against `MaxMemoryBytes`, until requests complete.
3. The response is read **item by item**. A bulk request is not all-or-nothing: a `200` can carry items the cluster rejected, which is a partial success, not an error. An item the cluster accepted receives `OnSuccess`.
4. An item that was rejected, or whose whole request failed, is classified by layer and kind (see [Retrying failed items](#retrying-failed-items)). If a retry policy requests a retry, the item returns to a buffer and is sent again; otherwise it receives `OnError`. See [Reading an error](#reading-an-error) for the contents of the error.
5. Each completed request also updates the controllers: its latency feeds the shard's controller, and a 429 feeds the node's.

When `Flush` returns, every item added before the call has had its `OnSuccess` or `OnError` called. This does not mean every item succeeded; the callbacks, or `OnUnhandledError` for an item with no `OnError`, report the items that did not.

All shards draw on the single `MaxMemoryBytes` limit, and a shard whose requests are slow keeps the bytes it holds until they are answered. To prevent one such shard from taking the whole limit, each shard is limited to an even share of it while other shards are waiting for room, and the waiting callers of other shards are served first. While no other shard is waiting, a shard may use the whole limit. A shard that holds nothing may always take one item, so that no shard is left with no work in progress; the limit can therefore be exceeded by at most one item for each such shard.

### Progress guarantees

The limits above delay work but cannot stop it. The following hold regardless of the cluster's state:

- A shard's buffer is never required to be smaller than 1 byte (`MinBytes` is held to at least 1, including under `ShardFixed`), and it is sealed once it holds `FlushBytes`. A buffer that holds an item has therefore always reached the smallest size it can be required to reach, and no buffer waits for a size it cannot reach. An item larger than the size is sent alone.
- A shard that holds nothing may always take one item, exceeding `MaxMemoryBytes` if necessary.
- When nothing is in flight to a node, one sealed buffer is sent regardless of its size, exceeding the node's window if necessary.

The memory limit and the node windows therefore make a caller wait for requests to complete, and never leave a shard or a node with nothing to send.

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

The indexer sends buffered data as bulk requests when a buffer is full, when `FlushInterval` (default 30 seconds) elapses, and when `Flush` or `Close` is called. The point at which a buffer is full depends on the sizing mode. By default sizing is adaptive: a controller sets the size of each shard's buffer from how quickly the node serves that shard's requests, up to `FlushBytes` (default 5 MiB). With `Sizing.Shard` set to `ShardFixed`, a buffer is full at exactly `FlushBytes`. See [Sizing batches](#sizing-batches).

`Body` is an `io.ReadSeeker` because the indexer rewinds it. The indexer reads the document from offset 0, whatever the reader's position when it is passed in, to size it, to fingerprint it, and for each attempt to send it, so every attempt, including a retry, sends the same bytes. A caller that tracks its own position in a reader should pass a reader of only the document, such as an `io.SectionReader`. Set `Index` on an item to send it to an index other than the configured default.

Behavior to be aware of:

- **`Add` after `Close` returns `ErrBulkIndexerClosed`.** Otherwise `Add` returns an error only when its context ends before the item is queued. It blocks while the indexer holds `MaxMemoryBytes`, and while its shard holds more than an even share of that limit with other shards waiting.
- **Documents of one shard keep their order.** Items that carry a `DocumentID` or a `Routing` value are written in the order they were added: a shard has one request in flight at a time, and a retry takes place in that request's slot, so a later buffer for the shard is not sent ahead of it. An item that the indexer cannot place in a shard, because there is no shard router or the index's shard map is not yet known, is ordered the same way by its ID: the buffer that holds it is not sent until the earlier buffer holding a document with the same ID has finished, while buffers that share no ID are sent in parallel.

  The order holds across a change in where documents are sent. A shard that moves to another node keeps its order, as does one whose node is unknown; only the next buffer's destination changes. A change in the index's shard geometry, as when the index is recreated, split or shrunk, or when the router loses the index's shard map, can send a document's next action to a different shard from its previous one. When `Add` detects such a change, the items still buffered for the index's shards fail through their own `OnError` with a `LayerIndex` error that matches `ErrShardMapChanged`, without being sent, and `Add` waits for the index's in-flight requests to be answered before it places the item, so that nothing overtakes them. Geometry changes are signaled by `ShardTarget.Generation`; a custom `ShardRouter` that returns 0 does not report them, and the indexer cannot detect them.

- **Callbacks run on the goroutine that sends the request.** A slow callback delays its buffer and the shard's next buffer, but not the rest of the indexer. Do not call `Add`, `Flush`, or `Close` from a callback; `Cancel` is safe.
- **Every item receives exactly one callback**: `OnSuccess` or `OnError`.

## Flush, Close, and Cancel

|          | Waits for                                                                     | Leaves the indexer open       | Purpose                                                |
| -------- | ----------------------------------------------------------------------------- | ----------------------------- | ------------------------------------------------------ |
| `Flush`  | the items added before the call, including retries                            | yes                           | drain at a checkpoint, or at the end of a unit of work |
| `Close`  | every item, including requests in flight and retry backoff                    | no                            | shut down cleanly                                      |
| `Cancel` | nothing: it cancels requests in flight, ends backoff, and returns immediately | no; `Close` is still required | shut down immediately                                  |

When `Flush` returns without a context error, every item it covered has already had its callback, so an external position can be committed from what the callbacks recorded. If the context ends first, `Flush` returns the context's error and stops waiting; the items still receive their callback, exactly once, possibly after `Flush` has returned.

`Close` blocks until every item is final. If its context ends first, it escalates to `Cancel`: the items not yet final fail, the requests unwind, and `Close` returns the context's error. To bound the wait, pass a context with a deadline; to avoid waiting, call `Close` in a goroutine.

### Closing an indexer

An indexer is more than a buffer. It runs a periodic flush on a timer and goroutines that send requests, and it holds pooled buffers and, when no `Client` was supplied, a client of its own. `Close` stops and releases all of these after sending what is still buffered. An indexer that is discarded without `Close` keeps its goroutines, timer and memory, and any item still in a buffer is lost when the process exits. `Flush` is not a substitute: it drains what has been added and leaves everything running. Call `Close` once, when the indexer is no longer needed, and reuse the indexer until then (see [A long-lived client](#a-long-lived-client)).

After `Cancel`, each item the indexer has not finished fails through its `OnError` exactly once. An item that was never sent fails with an error of layer `LayerIndexer` and kind `KindCanceled`, which matches `opensearchutil.ErrIndexerCanceled` and `context.Canceled`. An item that was in a request when the indexer was canceled fails with that request's error, which does not establish whether the item was written. `Add` after `Cancel` fails the item in the same way and returns nil.

## Reading an error

`OnError` receives a `*opensearchutil.BulkError`, which identifies the layer that failed (`Layer`) and the reason (`Kind`), and carries the details:

| Event                                                                 | `Layer`        | `Kind`                                         | Details                                                             |
| --------------------------------------------------------------------- | -------------- | ---------------------------------------------- | ------------------------------------------------------------------- |
| The cluster rejected this item (version conflict, mapping error, 429) | `LayerItem`    | by status                                      | `Resp` is the item's response and `Status` its status; `Err` is nil |
| The item was never sent (cannot serialize, no ID)                     | `LayerItem`    | `KindPermanent`                                | `Err` is the reason                                                 |
| The whole request failed (network error, 5xx, 429)                    | `LayerRequest` | by status, or `KindTransport` with no response | `Err` wraps the request error, and `Status` is the request's status |
| The index's shard map changed before the item was sent                | `LayerIndex`   | `KindPermanent`                                | `Err` matches `ErrShardMapChanged`                                  |
| The indexer was canceled before the item was sent                     | `LayerIndexer` | `KindCanceled`                                 | `Err` matches `ErrIndexerCanceled` and `context.Canceled`           |

`errors.Is` matches the error against the sentinel for its layer (`ErrItemLayer`, `ErrRequestLayer`, `ErrIndexLayer`, `ErrIndexerLayer`), the sentinel for its kind (`ErrRejected`, `ErrUnavailable`, `ErrTransport`, `ErrPermanent`, `ErrIndexerCanceled`), and its cause:

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

A request error is ambiguous: the request may have reached the cluster, in part or in full, before the error occurred. The indexer reports the same error to every item in the request. For an item with no `OnError`, the error is reported to `BulkIndexerConfig.OnUnhandledError` instead. An explicit `Flush` reports through its return value and does not call `OnUnhandledError`.

The transport also retries independently, beneath the indexer. It resends a request that failed with a network error or received a status in its `RetryOnStatus` (502, 503 and 504 by default), and, when the client has a router and the node's write thread pool is tracked, as with the default router, a request the node answered with a 429. The transport routes a resent 429 afresh, so it can reach a different node. See [Retry and Backoff](transport-retry_backoff.md). The indexer sees the error only after the transport's retries are exhausted, and a 429 that the transport resent successfully does not cut the node's window.

## Retrying failed items

Set `BulkIndexerConfig.RetryPolicy` to have the indexer resend failed items. The indexer runs the retry loop; the policy supplies the backoff and the limit:

```go
retry := opensearchutil.RetryPolicyFunc(func(attempt int, err *opensearchutil.BulkError) (time.Duration, bool) {
	if errors.Is(err, opensearchutil.ErrPermanent) || attempt >= 5 {
		return 0, false
	}
	return 100 * time.Millisecond << (attempt - 1), true // 100ms, 200ms, 400ms, ...
})
```

`attempt` counts the requests that have carried the work; it is 1 after the first request fails. The policy returns the delay and whether to retry. The indexer applies no limit of its own, so a policy that always returns true retries a persistent failure indefinitely.

### Layer and kind

A policy is consulted for two layers of error, identified by `err.Layer`:

- **`LayerRequest`**: the whole request failed. `err.Err` is the request's error. One call covers every item in the request.
- **`LayerItem`**: the cluster answered and rejected one item. `err.Resp` is that item's response. There is one call per rejected item.

Errors of the other layers, an index whose shard map changed and a canceled indexer, are never retried and are never passed to a policy. The indexer classifies each error by kind:

| Kind              | Sentinel             | Meaning                                                                                                              | Safe to retry               |
| ----------------- | -------------------- | -------------------------------------------------------------------------------------------------------------------- | --------------------------- |
| `KindRejected`    | `ErrRejected`        | a write queue was full (HTTP 429)                                                                                    | yes                         |
| `KindUnavailable` | `ErrUnavailable`     | the cluster or a proxy could not serve the request (HTTP 503)                                                        | yes                         |
| `KindTransport`   | `ErrTransport`       | the outcome is unknown: no response, a server error other than 503, or an unreadable response                        | only with deterministic IDs |
| `KindPermanent`   | `ErrPermanent`       | the same request would fail the same way: a mapping error, a version conflict, any other 4xx, an unpairable response | no                          |
| `KindCanceled`    | `ErrIndexerCanceled` | the indexer was canceled before the work was sent                                                                    | not offered to the policy   |

### Callbacks during retries

While an item is being retried, neither `OnSuccess` nor `OnError` runs for it. One of them runs once, when the indexer is done with the item: the item was accepted, the policy declined to retry it, or its retries were abandoned. A rejected item is resent in a request with the other items being retried, after the longest delay assigned to any of them.

Set `BulkIndexerConfig.Timeout`, the server-side timeout, whenever a `RetryPolicy` is set. Without it, a request abandoned by its context may continue running on the cluster while the retry sends a second request against the same shards; see [Bulk: Timeout Configuration](indexing-bulk.md#timeout-configuration).

The wait before a retry uses both a timer and the context. It ends early, and the waiting items fail with the error they last received, when the flush's context ends: the context passed to `Flush` or `Close`, or a call to `Cancel`. The buffer keeps its request slot while it waits, so the shard's next buffer is held back and its memory remains counted; keep delays short relative to `FlushInterval`.

## Document IDs

Retrying a `KindTransport` error can write a document twice, because the first attempt may have succeeded. With a deterministic `_id` the second write replaces the first; with a server-generated ID it creates a duplicate. Therefore, when a `RetryPolicy` is set, every item must have a `DocumentID`. An item without one is not sent and fails through its own `OnError` with an error that matches `opensearchutil.ErrMissingDocumentID`. `Add` still returns nil, and that `OnError` runs on the goroutine that called `Add`. Without a policy, behavior is unchanged and the cluster generates the ID.

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

`Add` calls the fingerprinter only for an item whose `DocumentID` is empty, before the item is placed in a shard, so a generated ID routes exactly as an explicit one does and per-ID ordering holds. The fingerprinter must be deterministic and safe for concurrent use. It may read `Body`; the indexer rewinds it. An error, or an empty ID, fails the item through its `OnError`.

## Carrying data with Metadata

`BulkIndexerItem.Metadata` is an opaque `any` that is returned unchanged in the item passed to `OnSuccess` and `OnError`. It is never sent to the cluster. Use it to carry what a callback needs, such as the stream offset of the document, instead of allocating a closure per item:

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

When indexing from a stream such as Kafka, a consumer may advance the stream's position past a record only once every earlier record is done. The indexer completes records out of order, because different shards finish at different times. A client library that marks the highest completed record would commit past a gap and lose the records in it.

`opensearchutil.FrontierTracker` reports each stream's frontier: the lowest offset that is not yet complete, below which every offset is complete. The frontier never passes a gap. Register where each stream starts, mark each record complete in `OnSuccess`, and read the frontier to commit:

```go
var tracker opensearchutil.FrontierTracker[string]
tracker.Begin("events/0", 100) // the first offset read

// In OnSuccess:
tracker.Complete(src.partition, src.offset)

// At each commit:
frontier, _ := tracker.Frontier("events/0") // every offset below frontier is done
```

The tracker has no failed state by design. A failed record must not advance the frontier, so `Complete` is not called for it: the frontier stays below the record until the application handles it, and the completions after it are held in memory until then.

Before releasing a stream, for example when a partition is revoked in a rebalance, perform these steps in order:

1. `Flush` the indexer, so that every record passed to it has completed or failed.
2. Call `tracker.Forget` and take the frontier it returns. Completions that arrive afterward are ignored.
3. Commit that frontier, and only that frontier.

This order is correct because the flush comes first and the frontier is read after it. Once `Flush` has returned and no more records are admitted for the stream, nothing further completes, so `tracker.Frontier` and `tracker.Forget` return the same value. Reading the frontier with `Frontier`, committing, and then calling `Forget` is equally safe, and keeps the stream tracked if the commit fails.

## Sizing batches

By default the indexer does not send every buffer at `FlushBytes`. `FlushBytes` is the most a shard's buffer collects, and two controllers operate beneath it, each controlling one quantity from one signal. `BulkIndexerConfig.Sizing` tunes them; the zero value selects defaults derived from `FlushBytes`.

|                                                         | Controls                                                      | Input                                                                               | Response                                                                 |
| ------------------------------------------------------- | ------------------------------------------------------------- | ----------------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| **Shard-buffer controller** (PID on latency, or fixed)  | how many bytes a shard's buffer collects before it is sent    | how fast the node served that shard's recent requests, relative to its other shards | a shard slower than the rest of its node: smaller buffers for that shard |
| **Node-window controller** (responds to 429s, or fixed) | how many bytes of requests may be in flight to a node at once | whether the node answered a request with a 429                                      | a node that is full: less in flight, then a gradual increase             |

### Why it is adaptive

A single indexer cannot know how much load a cluster can accept. The cluster is shared by many writers, its capacity changes as other work starts and stops, and a batch size that suits a node at one moment overloads it at another. A fleet of fixed-size writers either leaves capacity unused or fills the write queues and receives 429s. The indexer therefore measures, using two signals that require no coordination between clients:

- **429 responses** indicate that the server's write queue is full. When a node answers with one, the indexer reduces the number of bytes it keeps in flight to that node, then increases it in small steps while the node continues to accept writes. This is additive increase, multiplicative decrease (AIMD), the scheme TCP uses to find a network's capacity.
- **Write latency** distinguishes shards. A PID controller compares how quickly each node serves a shard's requests with the node's other shards, and gives a shard that is slower than the rest smaller buffers, so that a hot or expensive shard does not take the node's time from the others. A slowdown shared by the whole node reflects the node's load: no shard is reduced for it, and the node is sent as much as it accepts until it rejects.

Together they keep a node busy without queueing it into rejections. Because every client in the fleet makes the same adjustments from the same signals, their combined load stays near what the cluster can accept.

The two controllers matter to different workloads. The node window matters to any workload that can saturate a node: it turns a 429 into reduced load rather than a burst of retries. The shard-buffer controller compares a shard only with the other shards on the same node, so it matters only when shards differ. A hot shard, a shard whose documents are expensive to index (large, many fields, heavy ingest pipelines), or a shard on a node that is also merging or recovering, is slower than its neighbors, and full-size requests for it take the node's time from the other shards. The controller gives that shard smaller buffers, so it receives its share of the node's time and no more. When every shard on a node is about as fast as the others, which is typical for one index with evenly distributed IDs, nothing is reduced: each buffer stays at `FlushBytes`, and adaptive sizing behaves like `ShardFixed`, except that a shard joining a node that is already loaded starts at `InitialBytes` and increases.

#### The shard-buffer controller

The controller's input is the time each request spent on the node: the round trip minus the network distance. A request carries one shard, so this measures that shard's own speed. The controller compares the time with what the node at its best recent performance would need for a request of the same size: a fixed cost per request, plus the bytes at the best service rate observed recently. The fixed cost is learned from small requests, in which it is a visible part of the time. Without it, a small request would be judged against the throughput of large ones, which it cannot reach even on a healthy node. The comparison is smoothed over the last several flushes, because the time of a single flush is dominated by noise: queueing, garbage collection, merges. The smoothed level is then compared with the mean level of the node's other shards. A shard alone on its node, such as the buffer for documents that belong to no shard, is compared with itself and keeps its size.

- On a node that shows no load, a shard starts at `FlushBytes`, so a node that can accept full requests receives them from the first request. On a node that is already loaded (its write pool rejecting, or its window cut by a 429 and not yet restored), a shard starts at `InitialBytes` (`FlushBytes/4` by default) and increases to the full size as the node keeps pace. Setting `InitialBytes` starts every shard there. To send `FlushBytes` always, with neither the increase nor any reduction, set `Sizing.Shard` to `ShardFixed`, or `OPENSEARCH_GO_BULK_SHARD=fixed`.
- While the smoothed time stays within `Headroom` of the expected time, the buffer grows by `StepBytes` per flush.
- When the shard is slower than the rest of its node by more than that, its buffer is reduced in proportion to how far it exceeds `Headroom`, so that it yields to the node's other shards.
- `Headroom` is widened by the noise of the signal. A node whose times vary must slow by more than its usual variation before the slowdown counts, while a steady node is held to `Headroom` alone.
- The recorded best rate decays slowly while requests are large, and faster while they are small. A node that is slow even for small requests was not slowed by request size, so the buffer can recover on a node whose performance has changed.

The controller reads a request only if the node indexed at least one of its items. It ignores the duration of every other request, because none of them measures how fast the node indexes: a 429, which a node answers immediately because it did no work; a request that failed without a response, or with a 5xx or a 4xx that says nothing about this shard; and a request in which every item failed, such as one for a shard whose primary is not active, which fails only after the bulk `timeout` elapses. Treating any of these as a slow or fast flush would shrink or grow the buffer for a reason the buffer size cannot address. Items refused on their own merits, such as a version conflict or a mapping error, do not prevent a request from being read, provided some other item was indexed.

One failure does change a buffer. A **413**, a request refused for exceeding the size the cluster accepts (`http.max_content_length`), reflects the cluster's settings rather than its load. From it the indexer learns that no later request may be larger than half of the refused size, and it never raises that limit, so it stops sending requests that cannot be accepted and does not grow its buffers back to the refused size.

The size of each step comes from a PID controller on the node's normalized slowdown, with gains `Kp`, `Ki` and `Kd` (defaults `1`, `0.1` and `0.25`). [How the PID controller works](#how-the-pid-controller-works) describes the controller.

#### The node-window controller

The window is a number of bytes: the sum of the sizes of the requests the indexer has sent to a node and not yet had answered. A sealed buffer that would exceed the window waits for requests to complete, and is sent when it fits. When nothing is in flight to the node, one buffer is sent regardless of its size, so a node is never starved. A waiting buffer still counts against `MaxMemoryBytes`, which is what makes `Add` wait. The window limits the bytes in flight, not the size of any buffer, and it is separate from the number of requests in flight at once. That number follows the node's write thread pool, as the client's transport tracks it. For a node with no reported pool, such as the one that represents the whole cluster when the indexer has no shard map, `NumWorkers` requests may be in flight at once, or `GOMAXPROCS` when `NumWorkers` is 0. `GOMAXPROCS` is used rather than the machine's CPU count because, since Go 1.25, it follows a container's cgroup CPU limit (for programs whose `go.mod` declares 1.25 or later; older programs can set `GODEBUG=containermaxprocs=1`), and it follows the `GOMAXPROCS` environment variable.

The only signal is a 429: a response in which the node rejected the request, or any item in it.

- **The window starts fully open**, at `MaxMemoryBytes`, or at `NodeBytes` if set.
- **A 429 halves the bytes in flight.** The window becomes `Backoff` (one half) times the smaller of the window and the bytes that were in flight to the node when the response arrived, including the rejected request. Cutting from the bytes in flight rather than from the window makes the first 429 effective: a window that starts at the job's whole memory limit is far above what is ever in flight, and halving it would change nothing. Rejections of requests sent before the cut belong to the same event and do not cut the window again. The window never falls below one full buffer.
- **The window grows back gradually.** It grows by one `FlushBytes` for each window's worth of bytes the node answers, after a hold of a few responses, and not while the node's write pool is overloaded. A window that was cut recovers over many requests, and one that has not been cut barely changes.
- **The window does not record where the last 429 occurred.** A 429 can come from other writers to the cluster, or from a cluster that has since gained capacity, so the window continues to probe and settles into a sawtooth around what the server currently accepts.
- **A request that fails with no response is not a 429** and does not cut the window.

As a result, a node that slows down without rejecting keeps its shards' buffer sizes and its window, a shard slower than the rest of its node receives smaller buffers, and a node that rejects has fewer bytes in flight, regardless of its speed.

#### Starting a shard

A shard that starts on a node, such as the first document for a new index, is given the best service rate the node has shown; the node keeps that rate for all of its shards. Only the rate is carried over: the shard learns its own fixed cost per request. Its first flushes are then compared with the expected time for a request of that size, not with its own first flush, so a shard that starts while the node is busy does not adopt the busy rate as its best. The first shard on a node to send a request has no such history and learns its own rate.

### How the PID controller works

A PID controller is the mechanism behind a car's cruise control. Given a target speed, the controller measures the actual speed, computes the difference (the _error_), and sets the throttle. It does so with three terms, each scaled by a _gain_:

| Term             | Gain | Responds to                          | Effect                                                                                                                                             |
| ---------------- | ---- | ------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| **P**roportional | `Kp` | the current error                    | The correction is proportional to the size of the error.                                                                                           |
| **I**ntegral     | `Ki` | the error summed over recent flushes | A small, persistent error accumulates and adds to each correction, so the controller does not settle at a steady offset.                           |
| **D**erivative   | `Kd` | the rate at which the error changes  | A shard that suddenly falls behind is reduced further before the proportional term responds; a shard that is recovering is given more room sooner. |

The three results are summed into one number, `u`, which determines the step.

In the indexer, the controlled quantity is the size of one shard's buffer, and the measurement is the shard's _level_: the time the node spent on its requests, relative to what the shard at its best recent performance would need for a request of that size, smoothed over the last several flushes and compared with the level of the node's other shards. A shard alone on its node is compared with itself. The error `e` is 1 for a shard as fast as the rest of its node, 0 when it is slower by `Headroom` (25% by default, widened by the noise of the signal, referred to below as the slack), and -1 when it is slower by twice `Headroom` plus the slack or more. The slack is added once, so the slowdown that reaches -1 is `2 × Headroom + slack`:

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

The step is `u = Kp·e + Ki·(sum of recent e) + Kd·(e - previous e)`. A positive `u` grows the buffer by `StepBytes` scaled by `min(u, 1)`. A negative `u` multiplies it by `1 + max(u, -0.5)`, so a single flush never removes more than half. Over time the buffer grows while the shard keeps pace with the rest of its node and shrinks as it falls behind:

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

(The second graph illustrates the shape; it is not a measurement.)

The defaults suit most clusters. When changing them, change one at a time:

| Goal                                  | Change                           | Risk                                             |
| ------------------------------------- | -------------------------------- | ------------------------------------------------ |
| Faster growth and larger reductions   | raise `Kp`                       | oscillation: the size alternating up and down    |
| Less reaction to a single noisy flush | lower `Kp`, or set `Kd` to `0`   | slower response to a real slowdown               |
| No memory of past flushes             | set `Ki` to `0`                  | a mild, persistent slowdown left uncorrected     |
| No adaptive shard sizing              | `OPENSEARCH_GO_BULK_SHARD=fixed` | a hot shard continues to send full-size requests |

A gain of `0` disables that term. The environment overrides are listed in [Environment variable overrides](#environment-variable-overrides).

### Turning a controller off

The two controllers are independent, and each can be disabled separately:

| Setting                                       | Values                                                                                                                                                                                                                           | Default    |
| --------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------- |
| `Sizing.Shard`, or `OPENSEARCH_GO_BULK_SHARD` | `ShardPID` (`pid`): a shard's buffer follows how quickly the node serves its requests. `ShardFixed` (`fixed`): every buffer is `FlushBytes`, and the PID gains have no effect.                                                   | `ShardPID` |
| `Sizing.Node`, or `OPENSEARCH_GO_BULK_NODE`   | `NodeAIMD` (`aimd`): a node's window is cut when the node answers with a 429, and grows back gradually. `NodeFixed` (`fixed`): the window stays where it started, at `NodeBytes` or `MaxMemoryBytes`, and a 429 does not cut it. | `NodeAIMD` |

The zero values, `ShardUnset` and `NodeUnset`, select the defaults. With both disabled, every buffer is sent at `FlushBytes` and the window never changes. In either case, the number of requests in flight to a node still follows the node's write pool, as described under [The node-window controller](#the-node-window-controller).

A gain set to `0` through its environment variable disables that term. In code, a `Sizing.PID` left at its zero value selects the default gains, so all three cannot be set to `0` in code; through the environment they can, in which case no step changes the size. To keep shard buffers at a constant size, use `ShardFixed` instead.

For example, to send every buffer at `FlushBytes` while still backing off a node that answers with 429s:

```go
indexer, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
	Client:     client,
	FlushBytes: 5 << 20, // 5 MiB: every request a shard's buffer sends
	Sizing:     opensearchutil.BulkSizing{Shard: opensearchutil.ShardFixed},
})
```

### Why two different controllers

The controllers respond to different kinds of signal. A shard buffer's signal is a measurement, the time each flush took, and it varies by degree: a shard can be slightly or much slower than its neighbors, briefly or for a long time. A PID controller uses all of that information: a small slowdown produces a small reduction and a large one a large reduction (`Kp`), a persistent slowdown produces a larger reduction on each flush (`Ki`), and a sudden one is addressed before it accumulates (`Kd`).

A node window's signal is binary: the node answered with a 429, or it did not. With no magnitude to scale a step by, the window uses additive increase, multiplicative decrease: it increases in small steps while the node accepts writes and decreases sharply when the node refuses one, the scheme TCP uses to find a network's capacity.

This AIMD is unrelated to the AIMD congestion windows the transport keeps for connection scoring, described in [Request Routing](transport-routing.md#aimd-congestion-windows). They share the name and the additive-increase, multiplicative-decrease shape, and nothing else: the transport's windows select the connection a request uses, and this one limits the bytes in flight to a node.

### Environment variable overrides

Each of these settings can also be set from the environment, so an operator can change how a deployment sizes its batches without rebuilding it. `NewBulkIndexer` reads the variables once. A variable set to a valid value takes precedence over the code; a variable that is unset, empty or invalid leaves the code's setting in place.

| Variable                                  | Overrides                     | Values                                     |
| ----------------------------------------- | ----------------------------- | ------------------------------------------ |
| `OPENSEARCH_GO_BULK_SHARD`                | `Sizing.Shard`                | `pid`, `fixed`                             |
| `OPENSEARCH_GO_BULK_NODE`                 | `Sizing.Node`                 | `aimd`, `fixed`                            |
| `OPENSEARCH_GO_BULK_PID_KP`, `_KI`, `_KD` | `Sizing.PID.Kp`, `.Ki`, `.Kd` | non-negative number; `0` disables the term |

The three gains tune the shard-buffer PID controller and have no effect when `OPENSEARCH_GO_BULK_SHARD=fixed`. After each flush the controller computes `e`, the shard's distance from `Headroom` (`+1` as fast as the rest of its node, `0` at `Headroom`, `-1` at twice `Headroom` or slower), and takes a step `u = Kp·e + Ki·(sum of recent e) + Kd·(change in e)`. A positive step grows the buffer by up to `StepBytes`; a negative one shrinks it, by at most half in one flush.

- **`Kp`, the proportional gain (default `1`)**: sets the part of each step proportional to `e`, the shard's distance from `Headroom` on the current flush. Doubling `Kp` doubles that part: the buffer grows faster while the shard keeps pace and shrinks further once it falls behind. Halving it makes both changes smaller.
- **`Ki`, the integral gain (default `0.1`)**: adds to each step in proportion to the sum of `e` over recent flushes, capped at ±2. While the shard stays on one side of `Headroom`, the sum adds to every step, so a persistent slowdown shrinks the buffer further on each flush, and a sustained run of fast flushes grows it faster. At `0` it adds nothing.
- **`Kd`, the derivative gain (default `0.25`)**: adds to each step in proportion to the change in `e` since the previous flush. A sudden slowdown adds a reduction on the flush where it appears, before the other two terms respond, and a recovery adds growth. Noise between flushes also changes `e`, so a large `Kd` makes the size fluctuate on a noisy node; in that case lower it or set it to `0`.

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

A service that reads from a stream such as Kafka and writes to OpenSearch should read less when OpenSearch is slow. Reading too much and then crashing lengthens recovery, because every record read and not yet committed is replayed. `Add` alone is a poor signal for this: it blocks only once the whole `MaxMemoryBytes` is in use, and a blocked `Add` stalls the poll loop, which can exceed the stream's poll timeout and trigger a rebalance.

`GetStats` provides better signals. It loads the indexer's statistics into a caller-owned struct that the caller reuses, so it allocates nothing and cannot change the indexer, and it takes no lock, so it can be called at any frequency while other goroutines call `Add`:

```go
var stats opensearchutil.BulkIndexerStats // reused: GetStats replaces everything in it
if err := indexer.GetStats(&stats); err != nil {
	return err
}

fill := stats.BudgetFill()          // 0 to 1: how much of MaxMemoryBytes is held
typical := stats.Slowdown.Median    // how much longer than expected the typical node takes
pushedBack := stats.NumRejections   // only grows: compare with the last reading
```

| Reading         | Changes when                                                                                               | Use                                                                                                     |
| --------------- | ---------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| `BudgetFill()`  | the indexer holds more than the cluster accepts                                                            | slow down gradually, well before `Add` blocks                                                           |
| `NumRejections` | a node answers with a 429                                                                                  | back off immediately; an increase since the last reading means the cluster pushed back, however briefly |
| `NodeWindow`    | a node answers with a 429 (decreases) or accepts writes again (increases)                                  | distinguish congestion from recovery: 1 is clear                                                        |
| `Slowdown`      | a node takes longer for its requests than it needs                                                         | detect slowness directly, before any node rejects: 0 is as expected, 1 is twice as long                 |
| `BufferSize`    | the indexer finds a node's shards slower than the rest of the node (decreases) or keeping pace (increases) | measure how far the indexer has already reduced its own batches                                         |

`NodeWindow`, `Slowdown` and `BufferSize` are each a `JobNodeStats`: the reading across the nodes the indexer writes to, as `Min`, `Median`, `Max` and `Mean`. A single value is misleading. The extreme alone lets one node represent the whole cluster, and the average alone hides that node. Steer by `Median`, or by `Mean`, which weights each node by the bytes the indexer holds for it, and use `Min` or `Max` to detect an outlier. In a cluster that is slow overall, the median is near the worst node. A single slow node moves `Min` or `Max` and leaves the median unchanged; if that node also carries much of the work, the mean moves with it.

The consumer must also bound its own backlog: the number of records it has read and cannot yet commit. With a [`FrontierTracker`](#committing-a-stream-position), the backlog is the offset read minus `Frontier`, which is exactly what a crash would replay. A simple consumer keeps the backlog under a limit, pauses its partitions when `BudgetFill` passes a high threshold or `NumRejections` increases, shrinks its read buffer as `Slowdown.Median` or `Slowdown.Mean` grows, and resumes below a low threshold. The thresholds are application-specific; a wider gap between them pauses less often.

These readings follow the cluster and do not change the indexer's behavior; the indexer continues to adapt to the same signals independently. A consumer that adapts to them forms a second control loop over the same resource, so give it a longer time constant than the indexer's, or the two loops will oscillate against each other. Each value is smoothed over a node's last few requests.

## Observing the indexer

`BulkIndexerStats` counts what the indexer has done, including `NumRetries`, indexed by `ErrorKind`. For a stream of events instead, set `BulkIndexerConfig.Observer`, which follows the same pattern as the [transport's observer](transport-observer_metrics.md): embed `opensearchutil.BaseBulkIndexerObserver` and override only the required methods.

| Method           | Called                                                                                 |
| ---------------- | -------------------------------------------------------------------------------------- |
| `OnRequestEnd`   | after each bulk request returns, including a retry; carries status, duration, and size |
| `OnRequestError` | when a whole request fails, once per attempt; the error's `Layer` is `LayerRequest`    |
| `OnItemError`    | when one item fails, once per attempt; the error's `Layer` is `LayerItem`              |
| `OnItemDone`     | once per item, when the indexer is done with it; carries the size of its document      |

`OnRequestEnd` reports the size of the whole request body, which is the buffer the indexer built: the action lines and documents of one shard, before the transport compresses it. `OnItemDone` reports one document: `BulkItemEvent.Bytes` is the length of the item's `Body`, excluding the action line, and is the same whether the item succeeded, was rejected or was refused. It identifies an outlier that the request average hides. The compressed size on the wire is reported by the transport, not the indexer: the transport observer's `OnRequestStart` event carries the body size before compression, and `OnRequestResponse` the `Content-Length` after it.

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

`GetStats` provides the inputs for RED (rate, errors, duration) and USE (utilization, saturation, errors) metrics without an observer, and is suitable for a periodic gauge callback. Each field of `BulkIndexerStats` is documented as a COUNTER, which is monotonic, or a GAUGE, which reports the current value. Derive a rate from the difference between two readings of a counter, and a mean by dividing one counter by another.

| Metric                            | Fields                                                                       |
| --------------------------------- | ---------------------------------------------------------------------------- |
| Item and request rate             | `NumFlushed`, `NumFailed`, `NumRequests`                                     |
| Batches by flush trigger          | `NumByteFlushes`, `NumTimerFlushes`, `NumPressureFlushes`, `NumCloseFlushes` |
| Errors by layer and kind          | `Errors[layer][kind]`, for example `Errors[LayerRequest][KindRejected]`      |
| Mean and maximum item latency     | `ItemLatencyNanos / (NumFlushed + NumFailed)`, `MaxItemLatencyNanos`         |
| Mean and maximum request duration | `RequestDurationNanos / NumRequests`, `MaxRequestDurationNanos`              |
| Mean and maximum document size    | `ItemBytes / NumAdded`, `MaxItemBytes`                                       |
| Mean request size                 | `RequestBytes / NumRequests`                                                 |
| Utilization                       | `InFlightRequests`, `InFlightBytes`, `BufferedBytes`, `BudgetFill()`         |
| Saturation                        | `QueuedBuffers`, `BlockedShards`, `NumBlockedAdds`, `BlockedAddDuration`     |

Item latency runs from `Add` to the item's outcome, across every attempt. It includes time spent in a buffer and in the queue, so it exceeds request duration; the difference is queueing delay. `BlockedShards` counts the shards that have a request in flight and further documents waiting behind it. `GetStats` loads each value separately, so the values are not a snapshot of a single instant.

`GetNodeStats` reports the same readings for each node: in-flight requests and bytes, queued buffers, held bytes, rejections, window, buffer size and slowdown. It identifies the node behind an outlier in `NodeWindow`, `BufferSize` or `Slowdown`. Pass back the slice it returned to avoid allocating:

```go
var nodes []opensearchutil.NodeStats
nodes = indexer.GetNodeStats(nodes)
for _, n := range nodes {
	slowdown.WithLabelValues(n.Name).Set(n.Slowdown)
}
```

Like `GetStats`, `GetNodeStats` takes no lock, and one node's values are not guaranteed to be from the same instant.

## See also

- [Bulk](indexing-bulk.md) - the underlying `bulk` API, timeouts, and ingest-node routing.
- [Error Handling and Partial Failures](usage-error_handling.md) - how partial failures surface from the typed API.
- [Retry and Backoff](transport-retry_backoff.md) - the transport's own retries, beneath the indexer's.
- [Environment Variables](config-envvars.md#bulk-indexer) - the `OPENSEARCH_GO_BULK_*` overrides for batch sizing.
- [Observer-Based Metrics](transport-observer_metrics.md) - the transport observer model and the `osprom` and `osotel` modules.

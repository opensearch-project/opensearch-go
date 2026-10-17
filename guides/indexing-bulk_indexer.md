# Bulk Indexer

> **Runnable example:** [`_samples/indexing-bulk_indexer.go`](../_samples/indexing-bulk_indexer.go)

The [Bulk guide](indexing-bulk.md) sends one `bulk` request that you build and check yourself. `opensearchutil.BulkIndexer` sits on top of it for continuous ingestion: you hand it documents one at a time, and it keeps a buffer for each shard, sends each buffer to the node hosting the shard, and tells you what happened to each document through callbacks.

The retry policy, observer, document-id enforcement, fingerprinter, `Metadata`, `Cancel`, and `SequenceTracker` described below are new in v5.1.0.

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
	OnFailure: func(ctx context.Context, item opensearchutil.BulkIndexerItem, resp opensearchapi.BulkRespItem, err error) {
		fmt.Printf("failed %s: %v (status %d)\n", item.DocumentID, err, resp.Status)
	},
})
if err != nil {
	return err
}

// Send what has been added so far and keep the indexer open.
if err := indexer.Flush(ctx); err != nil {
	return err
}

// Wait for every item to reach a final state, then release the indexer.
if err := indexer.Close(ctx); err != nil {
	return err
}
fmt.Printf("%+v\n", indexer.Stats())
```

The indexer sends a shard's buffer when it reaches the size the indexer chose for that shard, at most `FlushBytes` (default 5 MB), every `FlushInterval` (default 30 seconds), and when you call `Flush` or `Close`. `Body` is an `io.ReadSeeker`, because the indexer rewinds it after reading. Add `Index` to an item to send it to an index other than the config default.

Things to know:

- **`Add` after `Close` returns `ErrBulkIndexerClosed`.** `Add` otherwise returns an error only when the context you pass ends before the item is queued; it blocks while the indexer holds `MaxMemoryBytes`.
- **Documents of one shard keep their order.** Items that carry a `DocumentID` or a `Routing` are written in the order you added them: a shard has one request out at a time, and a retry happens in that request's slot, so a later buffer of the shard is not sent ahead of it. An item the indexer cannot place in a shard, because there is no shard router or the index's shard map is not known yet, is ordered the same way by its id: the buffer holding it is not sent until the earlier buffer that holds a document with the same id has finished, while buffers that share no id are sent in parallel.
- **Callbacks run on the goroutine that sends the request.** A slow callback stalls its buffer, and the shard's next one, but not the rest of the indexer. Do not call `Add`, `Flush`, or `Close` from a callback; `Cancel` is safe.
- **Every item gets exactly one final callback**: `OnSuccess` or `OnFailure`.

## Flush, Close, and Cancel

|          | Waits for                                                                          | Leaves the indexer open | Use it to                                              |
| -------- | ---------------------------------------------------------------------------------- | ----------------------- | ------------------------------------------------------ |
| `Flush`  | the items added before the call, including retries                                 | yes                     | drain at a checkpoint, or at the end of a unit of work |
| `Close`  | every item, including requests in flight and retry backoff                         | no                      | shut down cleanly                                      |
| `Cancel` | nothing: it cancels requests in flight and ends backoff, and returns straight away | no, still call `Close`  | shut down now                                          |

When `Flush` returns without a context error, every item it covered has already had its callback, so you can commit an external position from what the callbacks recorded. If the context ends first, `Flush` returns that error and stops waiting; the items still get their callback, exactly once, but possibly after it returned.

`Close` blocks until every item is final. If its context ends first it escalates to `Cancel`: the items not yet final fail, the requests unwind, and `Close` returns the context's error. To bound it, pass a context with a deadline; to avoid waiting, run it in a goroutine.

After `Cancel`, each item that is not yet final fails through its `OnFailure` exactly once. An item that was never sent fails with `opensearchutil.ErrIndexerCanceled`, which wraps `context.Canceled`. An item that was in a request when it was canceled fails with that request's error, which is ambiguous about whether it landed. `Add` after `Cancel` fails the item the same way and returns nil.

## Three kinds of failure

`OnFailure` covers three different situations. Tell them apart by its arguments:

| What happened                                               | `err`                               | `resp`                                 |
| ----------------------------------------------------------- | ----------------------------------- | -------------------------------------- |
| The cluster rejected this item (version conflict, 429)      | `nil`                               | the item's response, with `Status` set |
| The whole request failed (network error, 5xx)               | non-nil, wrapping the request error | empty                                  |
| The item was never sent (cannot serialize, no id, canceled) | non-nil                             | empty                                  |

A request failure is ambiguous: the request may have reached the cluster, in part or in full, before the error. The indexer reports the same thing to every item in the request. If an item has no `OnFailure`, the failure is reported to `BulkIndexerConfig.OnError` instead. An explicit `Flush` reports through its return value and does not call `OnError`.

The transport also retries on its own, beneath the indexer, a request that drew a 502, 503, or 504 or failed on a network error; see [Retry and Backoff](transport-retry_backoff.md). The indexer only sees the failure once that is exhausted.

## Retrying failed items

Set `BulkIndexerConfig.RetryPolicy` to have the indexer send failed items again. The indexer drives the loop; the policy supplies the backoff and the limit:

```go
retry := opensearchutil.RetryPolicyFunc(func(
	attempt int,
	scope opensearchutil.FailureScope,
	kind opensearchutil.FailureKind,
	resp opensearchapi.BulkRespItem,
	err error,
) (time.Duration, bool) {
	if kind == opensearchutil.FailurePermanent || attempt >= 5 {
		return 0, false
	}
	return 100 * time.Millisecond << (attempt - 1), true // 100ms, 200ms, 400ms, ...
})
```

`attempt` counts the requests that have carried the work: 1 after the first request fails. The policy returns how long to wait and whether to retry. The indexer applies no limit of its own, so a policy that always returns true retries a persistent failure forever.

### Scope and kind

A bulk request can fail in two ways, and the policy is told which:

- **`ScopeRequest`**: the whole request failed. `resp` is empty and `err` is non-nil. One call stands for every item in the request.
- **`ScopeItem`**: the cluster answered and rejected one item. `resp` is that item's response and `err` is nil. There is one call per rejected item.

The indexer also classifies each failure:

| Kind                 | Means                                                                                                                | Safe to retry               |
| -------------------- | -------------------------------------------------------------------------------------------------------------------- | --------------------------- |
| `FailureRejected`    | a write queue was full (HTTP 429)                                                                                    | yes                         |
| `FailureUnavailable` | the cluster or a proxy could not serve it (HTTP 503)                                                                 | yes                         |
| `FailureTransport`   | the outcome is unknown: no response, a server error other than 503, or an unreadable response                        | only with deterministic ids |
| `FailurePermanent`   | the same request would fail the same way: a mapping error, a version conflict, any other 4xx, an unpairable response | no                          |

### What the callbacks see

While an item is being retried, neither `OnSuccess` nor `OnFailure` fires for it. Each fires once, when the item reaches a final state: accepted, declined by the policy, or abandoned. A rejected item is sent again in a request with the other items being retried, after the longest delay any of them was given.

Set `BulkIndexerConfig.Timeout`, the server-side timeout, whenever you set a `RetryPolicy`. Without it, a request that your context abandoned may keep running on the cluster while the retry adds a second one against the same shards; see [Bulk: Timeout Configuration](indexing-bulk.md#timeout-configuration).

The wait for a retry uses a timer and the context together. It ends early, and the items still waiting fail with the failure they last drew, when the context of the flush ends: the one you passed to `Flush` or `Close`, or `Cancel`. The buffer keeps its request slot while it waits, so the shard's next buffer is held back and the memory stays counted; keep delays short against `FlushInterval`.

## Document ids

Retrying a `FailureTransport` failure can write a document twice, because the first attempt may have landed. With a deterministic `_id` the second write replaces the first; with a server-generated one it adds a duplicate. So when a `RetryPolicy` is set, every item must have a `DocumentID`. An item without one is not sent and fails through its own `OnFailure` with `opensearchutil.ErrMissingDocumentID`. `Add` still returns nil, and that `OnFailure` runs on the goroutine that called `Add`. Without a policy, nothing changes and the cluster generates the id.

If your documents have no natural id, set `BulkIndexerConfig.Fingerprinter` to derive one:

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

The fingerprinter is called by `Add`, only for an item whose `DocumentID` is empty, before the item is placed in a shard, so a generated id routes exactly like an explicit one and per-id ordering holds. It must be deterministic and safe for concurrent use. It may read `Body`; the indexer rewinds it. An error, or an empty id, fails that item through its `OnFailure`.

## Carrying data with Metadata

`BulkIndexerItem.Metadata` is an opaque `any` that comes back unchanged in the item handed to `OnSuccess` and `OnFailure`. It is never sent to the cluster. Use it to carry what a callback needs, such as the stream offset the document came from, instead of allocating a closure per item:

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

If you index from a stream, such as Kafka, you may only advance the stream's position past a record once every record before it is done. The indexer completes records out of order, because different shards finish at different times. A client library that marks the highest completed record would commit past a gap and lose the records in it.

`opensearchutil.SequenceTracker` reports only the end of a contiguous prefix. Tell it where each stream starts, mark each record complete in `OnSuccess`, and read the position to commit:

```go
var tracker opensearchutil.SequenceTracker[string]
tracker.Begin("events/0", 100) // the first offset you read

// In OnSuccess:
tracker.Complete(src.partition, src.offset)

// Whenever you commit:
safe, _ := tracker.Safe("events/0") // every offset below safe is done
```

There is deliberately no failed state. A failed record must not advance the position, so do not call `Complete` for it: the position stays below it until you deal with it, and the completions after it are held in memory until you do.

Before you give a stream up, for example when a partition is revoked in a rebalance, do three things in this order:

1. `Flush` the indexer, so every record you handed it has completed or failed.
2. Call `tracker.Forget` and take the position it returns. Completions that arrive afterward are ignored.
3. Commit that position, and only that position.

What makes this safe is that the flush comes first and the position is read after it. Once `Flush` has returned and you are no longer admitting records for the stream, nothing further completes, so `tracker.Safe` and `tracker.Forget` return the same position. Reading it with `Safe`, committing, and then calling `Forget` is equally safe, and keeps the stream tracked if the commit fails.

## Observing the indexer

`BulkIndexerStats` counts what the indexer has done, including `NumRetries`, indexed by `FailureKind`. For a live stream of events instead, set `BulkIndexerConfig.Observer`, following the same pattern as the [transport's observer](transport-observer_metrics.md): embed `opensearchutil.BaseBulkIndexerObserver` and override only the methods you need.

| Method             | Fired                                                                                  |
| ------------------ | -------------------------------------------------------------------------------------- |
| `OnRequestEnd`     | after each bulk request returns, including a retry; carries status, duration, and size |
| `OnRequestFailure` | when a whole request fails, once per attempt; `Scope` is `ScopeRequest`                |
| `OnItemFailure`    | when one item fails, once per attempt; `Scope` is `ScopeItem`                          |
| `OnItemSettled`    | once per item, when it reaches its final state                                         |

The failure events carry the `FailureKind`, the attempt, and whether the retry policy is retrying. The methods run on the goroutine that sends the request and receive the context of the flush, so keep them quick, do not call back into the indexer, and let a tracing observer link to the flush's span.

The [`osprom`](https://github.com/opensearch-project/opensearch-go/tree/main/osprom) and [`osotel`](https://github.com/opensearch-project/opensearch-go/tree/main/osotel) modules ship ready-made observers that record bulk requests, failures by scope and kind, retries, and final item outcomes. They are independent of the transport's `Registry`:

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

## See also

- [Bulk](indexing-bulk.md) - the underlying `bulk` API, timeouts, and ingest-node routing.
- [Error Handling and Partial Failures](usage-error_handling.md) - how partial failures surface from the typed API.
- [Retry and Backoff](transport-retry_backoff.md) - the transport's own retries, beneath the indexer's.
- [Observer-Based Metrics](transport-observer_metrics.md) - the transport observer model and the `osprom` and `osotel` modules.

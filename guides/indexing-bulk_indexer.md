# Bulk Indexer

> **Runnable example:** [`_samples/indexing-bulk_indexer.go`](../_samples/indexing-bulk_indexer.go)

The [Bulk guide](indexing-bulk.md) sends one `bulk` request that you build and check yourself. `opensearchutil.BulkIndexer` sits on top of it for continuous ingestion: you hand it documents one at a time, and it keeps a buffer for each shard, sends each buffer to the node hosting the shard, and tells you what happened to each document through callbacks.

## A long-lived client

A `BulkIndexer` is meant to be a long-running, stateful client, not a helper you build for one request. It holds a buffer for each shard, it learns how fast and how loaded each node is, and it keeps its worker goroutines and timers running. Build one when your process starts, and keep it for as long as you are writing to the cluster.

Delimit your work into jobs with `Flush`, and reuse the indexer for the next job:

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

Creating an indexer per job throws away what the controllers learned about the nodes, so each new one starts from scratch, and each one that is not closed leaves its goroutines running. Share one indexer between goroutines instead: `Add` is safe for concurrent use.

## How the pieces fit together

| Term | Meaning |
| --- | --- |
| **Item** | One action (`index`, `create`, `update`, `delete`) on one document, with its callbacks and optional `Metadata`. It reaches a final state exactly once: `OnSuccess` or `OnFailure`. |
| **Job** | Not a type: the items you add between two calls to `Flush`. `Flush` is how you mark where one ends. |
| **Shard buffer** | The items bound for one shard, collected until the buffer is sent as one bulk request. |
| **Node limit** | How many bytes may be buffered for a node, across all the shard buffers it hosts. It bounds memory and is the indexer's answer to a node that rejects writes. |
| **Controllers** | The code that sets the two sizes above. The *shard-buffer controller* chooses when a shard's buffer is full, from write latency (a PID controller, or a fixed size). The *node-limit controller* sets the node limit from the node's 429 responses (AIMD). See [Sizing batches](#sizing-batches). |
| **Retry policy** | Optional. Decides, for each failed item, whether to send it again and after how long. See [Retrying failed items](#retrying-failed-items). |

The indexer owns them in this shape:

```
BulkIndexer
├── node A                  node limit: bytes buffered for the node         (AIMD on 429s)
│   ├── shard 0 buffer      sent when full; one request in flight per shard (PID on latency, or fixed)
│   └── shard 3 buffer
├── node B
│   └── shard 1 buffer
└── unplaced                items with no known shard, sent to the nearest node with room
```

An item travels through it like this:

1. `Add` places the item in the buffer for its shard. If the indexer already holds `MaxMemoryBytes`, `Add` waits until memory frees up.
2. A buffer is sent as one bulk request, to the node that hosts the shard's primary, when it is full (see [Basic use](#basic-use)), when `FlushInterval` passes, or when you call `Flush` or `Close`.
3. The response is read **item by item**. A bulk request is not all-or-nothing: a `200` can carry items the cluster rejected, and that is partial success, not an error. An item the cluster accepted gets `OnSuccess`.
4. An item that was rejected, or whose whole request failed, is classified by scope and kind (see [Retrying failed items](#retrying-failed-items)). With a retry policy that asks for a retry, the item goes back into a buffer and is sent again; otherwise it gets `OnFailure`. See [Three kinds of failure](#three-kinds-of-failure) for how to read its arguments.
5. Every completed request also teaches the controllers: the latency of the request feeds the shard's controller, and a 429 feeds the node's.

So `Flush` returning means every item you added has reached its final callback. It does not mean every item succeeded; the callbacks, or `OnError` for an item with no `OnFailure`, say which did not.

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

The indexer sends its buffered data as bulk requests when a buffer is full, when `FlushInterval` (default 30 seconds) passes, and when you call `Flush` or `Close`. What counts as full depends on the sizing mode. By default the indexer is adaptive: a controller picks the size of each shard's buffer from how quickly the node is serving that shard's requests, never more than `FlushBytes` (default 5 MiB). With `Sizing.Fixed`, a buffer is full at exactly `FlushBytes`. See [Sizing batches](#sizing-batches).

`Body` is an `io.ReadSeeker`, because the indexer rewinds it after reading. Add `Index` to an item to send it to an index other than the config default.

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

### Why you must close an indexer

An indexer is more than a buffer. It runs a periodic flush on a timer and goroutines that send requests, it holds pooled buffers and, when you did not pass a `Client`, a client of its own. `Close` is what stops and releases all of that, after sending what is still buffered. An indexer that is dropped without `Close` keeps its goroutines and timer alive, keeps its memory, and loses any item still sitting in a buffer when the process exits. `Flush` is not a substitute: it drains what has been added and leaves everything running. Call `Close` once, when you are done with the indexer for good, and reuse the indexer until then (see [A long-lived client](#a-long-lived-client)).

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

## Sizing batches

By default the indexer does not send every buffer at `FlushBytes`. It treats `FlushBytes` as a ceiling and steers the size of the batches sent for each shard, and the limit on what is buffered for each node, by what the node can take. `BulkIndexerConfig.Sizing` tunes this; the zero value selects defaults derived from `FlushBytes`.

### Why it is adaptive

A single indexer cannot know how much a cluster can take. The cluster is shared by many writers, its capacity changes as other work comes and goes, and a batch that is right for one node at one moment overloads it at another. A fleet of fixed-size writers either leaves capacity unused or fills the write queues and draws 429s. So the indexer measures instead, with two signals that need no coordination between clients:

- **429 responses** are the server's hard limit, the point at which its write queue is full. The indexer reacts to them with additive increase, multiplicative decrease (AIMD), the same scheme TCP uses to find a network's capacity: probe upward in small steps while the node accepts writes, and cut sharply when it refuses one.
- **Write latency** moves before a queue fills. A PID controller watches how quickly each node is serving a shard's requests and gives the shard smaller buffers as the node slows, so bulk traffic gives way before anything is rejected, and larger buffers while the node keeps up.

Together they aim to keep a node busy without queueing it into rejections, and since every client in the fleet makes the same adjustments from the same signals, the load they add together stays near what the cluster can take.

Two controllers work at two levels. A **shard's buffer** follows how quickly that shard's requests are served, with the PID controller below. A **node's limit**, the ceiling above the buffers of the shards it hosts, is learned from rejections alone and ignores how fast the node is. Two signals steer a shard's buffer:

- **A rejection** (a 429 from the node's write thread pool) cuts the size by `Backoff` at once, and holds it there for a few flushes. A request that fails for another reason cuts it by the square root of `Backoff`.
- **The node's service rate**, in bytes sent per second of time on the node (the round trip less the network distance), is compared with the best rate seen recently. While the rate stays within `Headroom` of that best, the size grows by `StepBytes` per flush. When the node slows by more than that, the size is cut in proportion to how far past `Headroom` it is, long before the node starts rejecting, so that bulk traffic gives way to the other requests the node is serving.

How hard each step is comes from a PID controller on the node's normalized slowdown, with gains `Kp`, `Ki` and `Kd` (defaults `1`, `0.1` and `0.25`). If you have not met a PID controller before, [How the PID controller works](#how-the-pid-controller-works) explains it.

The node's limit starts wide open, with the whole of the job's memory unless you set `NodeBytes` to start it lower, and the other nodes can only take it back down to its even share. A rejection cuts it by `Backoff` and holds it there for a few flushes, after which it grows again by a step per flush the node accepted, up to what the other nodes leave of the job. It does not remember where the last rejection was: a 429 can come from other writers to the cluster, or from a cluster that has since gained capacity, so the limit keeps probing and settles into a sawtooth around whatever the server accepts now. A request that fails for another reason cuts it by the square root of `Backoff`, and it is also cut gently when more was buffered for the node than it was allowed. It never goes below one full shard buffer. So a node that slows down without rejecting gets smaller buffers from its shards and keeps its limit, and a node that rejects has the memory held for it cut, whatever its speed.

A shard's buffer is compared with the best rate the node has shown, and the node keeps that best rate for all its shards. A shard that starts on a node, such as the first document for a new index, therefore starts from what the node has done and not from its own first flush, so a shard that starts while the node is busy does not take the busy rate for the best it can do. The first shard on a node to send anything has no such history and learns its own.

### How the PID controller works

A PID controller is the idea behind a car's cruise control. You pick a speed. The controller measures the actual speed, finds the difference (the *error*), and decides how hard to press the throttle. It does that with three terms, each scaled by a *gain*:

| Term | Gain | Reacts to | In plain terms |
| --- | --- | --- | --- |
| **P**roportional | `Kp` | the error now | How far off are we? The further off, the harder the correction. |
| **I**ntegral | `Ki` | the error summed over recent flushes | How long have we been off? A small error that will not go away builds up and pushes harder, so the controller does not settle for "a little slow" forever. |
| **D**erivative | `Kd` | how fast the error is changing | Is it getting better or worse, and how fast? A node that suddenly slows is cut harder, before the proportional term catches up; one that is recovering is given more room sooner. |

The three results are added up into one number, `u`, and `u` decides the step.

Here the thing being steered is the size of one shard's buffer, and the measurement is the *service rate* of the node: bytes sent per second of time the node spent on the request, compared with the best rate seen recently. The error `e` is 1 for a node as fast as its best, 0 when it has slowed by `Headroom` (25% by default), and -1 when it has slowed by twice that or more:

```
 e
+1 ●━━━━━━━╲                       node as fast as its best recent rate
           ╲
 0 ┈┈┈┈┈┈┈┈┈┈●┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈  slowdown = Headroom: hold the size
                ╲
-1               ●━━━━━━━━━━━━━━━  slowdown of 2 × Headroom or worse (clamped)
   └──────┬───────┬───────┬──────── slowdown
          0      0.25    0.5       (0 = best rate seen, 1 = twice as slow)
```

The step is `u = Kp·e + Ki·(sum of recent e) + Kd·(e - previous e)`. A positive `u` grows the buffer by `StepBytes` scaled by `min(u, 1)`. A negative `u` multiplies it by `1 + max(u, -0.5)`, so a single flush never takes away more than half. Over time the buffer climbs while the node keeps up and gives way as it slows:

```
buffer size
FlushBytes ┤            ●━━━━━━━●                      ceiling
           │          ╱         ╲                     
           │        ╱            ╲        ╱╲          node slows past Headroom:
           │      ╱               ╲      ╱  ╲╱━━━━     steps down in proportion,
           │    ╱                  ●╲  ╱               then settles where the
           │  ╱                       ●                node keeps up
 start     ┤●
           └─────────────────────────────────────── flushes
```

(The second graph is an illustration of the shape, not a measurement.)

The defaults suit most clusters. If you must change them, change one at a time:

| To get | Do | Watch for |
| --- | --- | --- |
| Faster growth and harder cuts | raise `Kp` | oscillation: the size swinging up and down |
| Less reaction to one noisy flush | lower `Kp`, or set `Kd` to `0` | slower response to a real slowdown |
| No memory of past flushes | set `Ki` to `0` | a mild, lasting slowdown going uncorrected |
| No PID at all | `OPENSEARCH_GO_BULK_AIMD=on` | a sawtooth around `Headroom` instead of an easing |

A gain of `0` turns that term off. The operator overrides are listed in [Changing it without a code change](#changing-it-without-a-code-change).

### Choosing a mode

| Mode | Set with | Behavior |
| --- | --- | --- |
| Adaptive, PID (default) | nothing | The size follows the node; each step is shaped by the PID controller. |
| Adaptive, AIMD only | `Sizing.AIMD`, or `OPENSEARCH_GO_BULK_AIMD=on` | Plain additive increase, multiplicative decrease: `StepBytes` more after a healthy flush, and the size multiplied by the square root of `Backoff` after a flush that shows the node slowing past `Headroom`. The PID gains are ignored. |
| Fixed | `Sizing.Fixed`, or `OPENSEARCH_GO_BULK_MODE=fixed` | The controller is off: every batch is `FlushBytes`, and a node's limit is its even share (`NodeBytes`). |

To soften the PID controller without dropping it, set a gain to `0`: `Ki` and `Kd` at `0` leave a proportional controller. Setting all three to `0` makes every PID step zero, so the size then changes only on a rejection or a failure; to drop the PID controller, use AIMD instead.

```go
indexer, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
	Client:     client,
	FlushBytes: 5 << 20, // 5 MiB: the largest request a shard's buffer grows to
	Sizing:     opensearchutil.BulkSizing{AIMD: true},
})
```

### How AIMD and the PID controller relate

They are two ways of taking the same step for a shard's buffer, and a flush uses one or the other, never both. The node's limit above the buffers is not affected by the choice: it is always additive increase and multiplicative decrease on rejections. Everything around the step is shared:

- A rejection cuts the size by `Backoff` and holds it for a few flushes. A failed request cuts it by the square root of `Backoff`, and a node that was given more than it takes is cut gently. None of these depends on the mode.
- Both read the same signal: the node's rate against the best recent rate, as a slowdown measured against `Headroom`. A node whose write pool is rejecting does not grow, and a flush too small to say anything is ignored.
- Both grow nothing during the hold after a rejection, and both keep the size between `MinBytes` and the ceiling.

What differs is the size of the step for a flush that completed without a rejection:

|                            | PID (default)                                                                                                           | AIMD (`Sizing.AIMD`)                                                                                                 |
| -------------------------- | ----------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| Healthy flush              | Grows by `StepBytes` scaled by `min(u, 1)`, so a node only just within `Headroom` grows by a fraction of a step         | Grows by exactly `StepBytes`; holds the size at exactly `Headroom` or while the node's write pool is rejecting       |
| Slow flush                 | Multiplies the size by `1 + max(u, -0.5)`: a node barely past `Headroom` loses little, one far past it loses up to half | Multiplies the size by the square root of `Backoff` (about 0.71 by default), however far past `Headroom` the node is |
| Where growth turns to cuts | Can sit on either side of `Headroom`, since the integral and derivative terms add to `u`                                | At `Headroom`                                                                                                        |
| Memory                     | The integral term remembers recent flushes; the derivative term remembers the last one                                  | None: each flush is judged alone                                                                                     |

So AIMD is the PID controller with the shaping taken out. It is simpler to reason about and easier to predict, but it treats a marginal slowdown like a severe one, and it settles into a sawtooth around `Headroom` instead of easing toward it. The PID controller damps that sawtooth, at the cost of three gains to tune. Reach for AIMD when the PID controller's behavior is hard to account for in production, and for the gains when it is close and wants adjusting.

This AIMD is unrelated to the AIMD congestion windows the transport keeps for connection scoring, described in [Request Routing](transport-routing.md#aimd-congestion-windows). They share a name and the additive-increase, multiplicative-decrease shape, and nothing else: that one steers which connection a request uses, and this one steers how many bytes a batch carries.

### Changing it without a code change

An operator can override these settings from the environment, which is how to back off the controller on a deployment you cannot rebuild:

| Variable                                  | Overrides               | Values                                      |
| ----------------------------------------- | ----------------------- | ------------------------------------------- |
| `OPENSEARCH_GO_BULK_MODE`                 | `Sizing.Fixed`          | `fixed`, `adaptive`                         |
| `OPENSEARCH_GO_BULK_AIMD`                 | `Sizing.AIMD`           | `on`, `off`                                 |
| `OPENSEARCH_GO_BULK_PID_KP`, `_KI`, `_KD` | `Sizing.Kp`, `Ki`, `Kd` | non-negative number; `0` turns the term off |

They are read once, when `NewBulkIndexer` builds the indexer, and a variable that is set and valid wins over the code. An empty, unset, or unrecognized value leaves the code's setting in place. The common cases:

```sh
# Stop adaptive sizing altogether: every batch is FlushBytes.
export OPENSEARCH_GO_BULK_MODE=fixed

# Keep adaptive sizing, but drop the PID controller.
export OPENSEARCH_GO_BULK_AIMD=on

# Keep the PID controller, but with no integral or derivative term.
export OPENSEARCH_GO_BULK_PID_KI=0 OPENSEARCH_GO_BULK_PID_KD=0
```

The [environment variable reference](config-envvars.md#bulk-indexer) lists every value.

## Steering a consumer by the indexer's load

If your service reads from a stream such as Kafka and writes to OpenSearch, it should read less when OpenSearch is slow. Reading too much and then crashing makes recovery longer, because everything read and not yet committed is replayed. `Add` alone is a poor signal for this: it blocks only once the whole `MaxMemoryBytes` is spent, and a blocked `Add` stalls your poll loop, which can pass the stream's poll timeout and trigger a rebalance.

`GetStats` gives a better one. It loads the indexer's statistics into a struct you own and reuse, so it allocates nothing and cannot change the indexer, and it takes no lock, so you can call it as often as you like while other goroutines call `Add`:

```go
var stats opensearchutil.BulkIndexerStats // reused: GetStats replaces everything in it
if err := indexer.GetStats(&stats); err != nil {
	return err
}

fill := stats.BudgetFill()     // 0 to 1: how much of MaxMemoryBytes is held
worst := stats.WorstNodeLimit  // 1 while every node is wide open, lower as a node rejects writes
pushedBack := stats.NumRejections // only grows: compare with the last reading
```

Three readings describe how the cluster is taking your writes:

| Reading | Moves when | Use it to |
| --- | --- | --- |
| `BudgetFill()` | the indexer holds more than the cluster takes | slow down smoothly, well before `Add` blocks |
| `NumRejections` | a node answers 429 | back off at once; a rise since your last reading means the cluster pushed back, however briefly |
| `WorstNodeLimit` | a node rejects writes (down) or accepts them again (up) | tell congestion from relief: below 1 the indexer is holding back, and a return to 1 says it is clear |

The second number to bound is your own: how many records you have read and not yet made safe to commit. With a [`SequenceTracker`](#committing-a-stream-position) that is the offset you have read minus `Safe`, and it is exactly what a crash would replay. A simple consumer keeps it under a limit, pauses its partitions when `BudgetFill` passes a high mark or `NumRejections` rises, and resumes below a low mark. The marks are yours: a wider gap between them pauses less often.

These readings follow the cluster and do not change the indexer's controllers, which go on adapting to the same signals without your help. A consumer that adapts by them is a second loop over the same resource, so give it a gentler time constant than the indexer's, or it and the indexer will chase each other.

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
- [Environment Variables](config-envvars.md#bulk-indexer) - the `OPENSEARCH_GO_BULK_*` overrides for batch sizing.
- [Observer-Based Metrics](transport-observer_metrics.md) - the transport observer model and the `osprom` and `osotel` modules.

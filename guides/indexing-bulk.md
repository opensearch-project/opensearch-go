# Bulk

> **Runnable example:** [`_samples/indexing-bulk.go`](../_samples/indexing-bulk.go)

This guide covers the `bulk` API: one request, built by the caller, that indexes, creates, updates or deletes many documents, and a response the caller checks item by item.

**Which one to use.** Use the `bulk` API directly when you already have a batch in hand and want to send it as one request, for example a one-off import or a test fixture. For continuous ingestion, where documents arrive one at a time and you want them batched, sent to the right node, retried and reported back per document, use [`opensearchutil.BulkIndexer`](indexing-bulk_indexer.md) instead. It builds these requests for you. The [timeout](#timeout-configuration) and [document ID](#use-client-assigned-document-ids-for-recoverability) advice below applies to both.

> **Surface note:** `BulkResp.Items` is a `[]BulkItem`. Each `BulkItem` has one field per operation (`Index`, `Create`, `Update`, `Delete`), and the one that is set is a `*BulkRespItem`. `BulkRespItem.ID` is a `*string`, so dereference it before formatting it. `BulkReq.Index` is a single `string`, the default `_index` for actions that do not name one. Requests that take several indices use `Indices []string`, as in `IndicesDeleteReq.Indices`. See [`opensearchapi/UPGRADING_V4_TO_V5.md`](../opensearchapi/UPGRADING_V4_TO_V5.md) for the full change from the hand-written v4 surface.

## Setup

First, create a client:

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

func main() {
	if err := example(); err != nil {
		fmt.Printf("Error: %s\n", err)
		os.Exit(1)
	}
}

func example() error {
	client, err := opensearchapi.NewDefaultClient()
	if err != nil {
		return err
	}

	ctx := context.Background()
```

Next, create two indices, `movies` and `books`, with the default settings:

```go
	movies := "movies"
	books := "books"

	createResp, err := client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: movies})
	if err != nil {
		return err
	}
	fmt.Printf("Index created: %t\n", createResp.Acknowledged)

	createResp, err = client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: books})
	if err != nil {
		return err
	}
	fmt.Printf("Index created: %t\n", createResp.Acknowledged)
```

## The bulk API

A bulk request body is newline-delimited JSON. Each operation is an action line, such as `{ "index": { "_index": "movies", "_id": 1 } }`, and, for every action except `delete`, the document on the line after it. The body must end with a newline.

### Indexing multiple documents

The following request indexes two documents in `movies` and one in `books`:

```go
	bulkResp, err := client.Doc.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Body: strings.NewReader(`{ "index": { "_index": "movies", "_id": 1 } }
{ "title": "Beauty and the Beast", "year": 1991 }
{ "index": { "_index": "movies", "_id": 2 } }
{ "title": "Beauty and the Beast - Live Action", "year": 2017 }
{ "index": { "_index": "books", "_id": 1 } }
{ "title": "The Lion King", "year": 1994 }
`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err := json.MarshalIndent(bulkResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Bulk Resp:\n%s\n", string(respAsJson))
```

### Creating multiple documents

`create` differs from `index` in that it fails for a document whose ID already exists, where `index` replaces it. The following request creates three documents in `movies` and one in `books`:

```go
	bulkResp, err = client.Doc.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Body: strings.NewReader(`{ "create": { "_index": "movies" } }
{ "title": "Beauty and the Beast 2", "year": 2030 }
{ "create": { "_index": "movies", "_id": 5 } }
{ "title": "Beauty and the Beast 3", "year": 2031 }
{ "create": { "_index": "movies", "_id": 6 } }
{ "title": "Beauty and the Beast 4", "year": 2049 }
{ "create": { "_index": "books" } }
{ "title": "The Lion King 2", "year": 1998 }
`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(bulkResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Bulk Resp:\n%s\n", string(respAsJson))
```

The first and last actions omit `_id`, so OpenSearch generates one, as the `create` API does. Had the other two reused IDs 1 or 2, which the previous request indexed, those items would have failed with a version conflict; see [Handling errors](#handling-errors).

### Updating multiple documents

```go
	bulkResp, err = client.Doc.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Body: strings.NewReader(`{ "update": { "_index": "movies", "_id": 1 } }
{ "doc": { "year": 1992 } }
{ "update": { "_index": "movies", "_id": 1 } }
{ "doc": { "year": 2018 } }
`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(bulkResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Bulk Resp:\n%s\n", string(respAsJson))
```

The line after an `update` action holds the change in `doc`: a full document, or only the fields to change.

### Deleting multiple documents

A `delete` action has no document line after it. Deleting a document that does not exist is not an error: the item's `result` is `not_found`.

```go
	bulkResp, err = client.Doc.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Body: strings.NewReader(`{ "delete": { "_index": "movies", "_id": 1 } }
{ "delete": { "_index": "movies", "_id": 2 } }
`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(bulkResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Bulk Resp:\n%s\n", string(respAsJson))
```

### Mixing operations

One request can mix operations. The following request creates two documents, updates one of them and deletes the other:

```go
	bulkResp, err = client.Doc.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Body: strings.NewReader(`{ "create": { "_index": "movies", "_id": 3 } }
{ "title": "Beauty and the Beast 5", "year": 2050 }
{ "create": { "_index": "movies", "_id": 4 } }
{ "title": "Beauty and the Beast 6", "year": 2051 }
{ "update": { "_index": "movies", "_id": 3 } }
{ "doc": { "year": 2052 } }
{ "delete": { "_index": "movies", "_id": 4 } }
`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(bulkResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Bulk Resp:\n%s\n", string(respAsJson))
```

### Handling errors

A bulk request is not all-or-nothing. The cluster answers `200` and reports each operation separately, so some items can succeed while others fail. Each item has a `status`: `2xx` when it succeeded, and otherwise an `error` that says why.

By default the client reports failed items as an error: `client.Doc.Bulk` returns a `*opensearchapi.PartialBulkError` along with a fully populated response. The error lists the failed items, and the response still holds every item. A partial failure is therefore an error the caller should inspect rather than return:

```go
	bulkResp, err = client.Doc.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Body: strings.NewReader(`{ "create": { "_index": "movies", "_id": 3 } }
{ "title": "Beauty and the Beast 5", "year": 2050 }
`),
		},
	)
	var partial *opensearchapi.PartialBulkError
	switch {
	case errors.As(err, &partial):
		for _, item := range partial.FailedItems {
			reason := ""
			if item.Error != nil && item.Error.Reason != nil {
				reason = *item.Error.Reason
			}
			fmt.Printf("Bulk item failed (status %d): %s\n", item.Status, reason)
		}
		fmt.Printf("%d items succeeded\n", partial.SucceededCount)
	case err != nil:
		return err // the request itself failed
	}
```

A client configured with `Config.Errors` to mask the `BulkItems` category returns no error for failed items, and the caller has to find them in the response instead. Each `BulkItem` has one operation field set, and that field's `Error` is set when the item failed:

```go
	for _, item := range bulkResp.Items {
		var op string
		var sub *opensearchapi.BulkRespItem
		switch {
		case item.Index != nil:
			op, sub = "index", item.Index
		case item.Create != nil:
			op, sub = "create", item.Create
		case item.Update != nil:
			op, sub = "update", item.Update
		case item.Delete != nil:
			op, sub = "delete", item.Delete
		}
		if sub != nil && sub.Error != nil {
			reason := ""
			if sub.Error.Reason != nil {
				reason = *sub.Error.Reason
			}
			fmt.Printf("Bulk %s Error: %s\n", op, reason)
		}
	}
```

[Error Handling and Partial Failures](usage-error_handling.md) covers the error mask, which failures are worth retrying, and how to monitor partial failures.

## Timeout configuration

A bulk request can run for a long time, especially a large one. Two separate timeouts bound it:

- **Server-side** (`BulkParams.Timeout`, sent as `?timeout=`): how long each action may wait for the cluster before the server gives up on it. An action waits when its index has to be created, when a dynamic mapping update is needed, or when its shard's primary is not active. The server's default is one minute. It bounds waiting only: a write that has started runs to completion.
- **Client-side** (the `context.Context`): how long the client waits for the answer before it cancels the request.

### Setting a server-side timeout

```go
	bulkResp, err := client.Doc.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Index: "movies",
			Body: strings.NewReader(`{ "index": { "_id": 1 } }
{ "title": "Beauty and the Beast", "year": 1991 }
{ "index": { "_id": 2 } }
{ "title": "Beauty and the Beast - Live Action", "year": 2017 }
`),
			Params: &opensearchapi.BulkParams{
				TimeoutParams: opensearchapi.TimeoutParams{Timeout: 30 * time.Second},
			},
		},
	)
	if err != nil {
		return err
	}
```

### Layering the two timeouts

Set the server-side timeout shorter than the client-side deadline. Then an action that is stuck waiting, for example on a shard whose primary is relocating, comes back as a failed item (`unavailable_shards_exception`, status 503) before the client gives up, and the client knows which items to retry.

```go
	// Client-side deadline: 60s.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Server-side timeout: 45s, shorter than the client's deadline.
	bulkResp, err := client.Doc.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Index: "movies",
			Body:  body,
			Params: &opensearchapi.BulkParams{
				TimeoutParams: opensearchapi.TimeoutParams{Timeout: 45 * time.Second},
			},
		},
	)
	if err != nil {
		return err
	}
```

When the client's deadline passes first, the client gets a context error and learns nothing about the items. The server does not see the cancellation: it goes on with the request, and some or all of the documents may be written.

### How blind retries lead to overload

That ambiguity is where overload starts. A writer that treats a deadline as transient and resends the whole batch tends to fail like this:

1. The client's deadline passes, it cancels the request and gets `context.DeadlineExceeded`.
2. The server goes on writing the batch to its shards.
3. The client sends the same batch again.
4. Routing by document ID is deterministic, so the retry reaches the same primary shards, which now have both requests to work through.
5. Each further retry adds another request for the same shards. Their write queues fill, and the server starts rejecting requests with `es_rejected_execution_exception` (HTTP 429).
6. Other clients writing to those shards get the same rejections, and their retries spread the overload further.

Three things keep a writer out of this loop: a server-side timeout shorter than the client's deadline, so waiting actions come back as failed items rather than as an unknown; client-assigned document IDs, so a retry can be limited to what failed and cannot duplicate what succeeded; and retries that back off, and slow down further on a 429. `BulkIndexer` does the last for you; see [Retrying failed items](indexing-bulk_indexer.md#retrying-failed-items).

### Use client-assigned document IDs for recoverability

When bulk actions carry no `_id`, OpenSearch generates the IDs. If the client's deadline passes before the response arrives, there is no way to tell which documents were written: some may exist under IDs the client never saw, and a retry writes them a second time under new IDs.

With an `_id` on every action, the outcome can be recovered. After a timeout or any other ambiguous failure, the client can look up the IDs it sent to find which documents were written, and retry only the rest. A retry of a document that was written replaces it rather than duplicating it.

```go
// With client-assigned IDs, the outcome of a failed bulk request is recoverable.
{ "index": { "_index": "events", "_id": "evt-20260226-0001" } }
{ "index": { "_index": "events", "_id": "evt-20260226-0002" } }

// After a timeout, check which documents exist before retrying:
//   GET /events/_mget { "ids": ["evt-20260226-0001", "evt-20260226-0002"] }
```

[Error Handling and Partial Failures](usage-error_handling.md) describes retry strategies that account for this.

## Routing bulk requests

With a router, the client sends each request to a node suited to it, and bulk requests (`/_bulk`) go to ingest nodes. `NewDefaultRouter` routes by request type and adds connection scoring; it is the recommended router. Routing needs node discovery, so the client knows the cluster's nodes and their roles:

```go
	router, err := opensearchtransport.NewDefaultRouter()
	if err != nil {
		return err
	}

	bulkClient, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: []string{"http://localhost:9200"},

			// Discover the cluster's nodes and their roles.
			DiscoverNodesOnStart:  new(true),
			DiscoverNodesInterval: 5 * time.Minute,

			Router: router,
		},
	})
	if err != nil {
		return err
	}

	// This request goes to an ingest node.
	bulkResp, err := bulkClient.Doc.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Body: strings.NewReader(`{ "index": { "_index": "movies", "_id": "perf-1" } }
{ "title": "High Performance Bulk", "year": 2024 }
{ "index": { "_index": "movies", "_id": "perf-2" } }
{ "title": "Optimized Ingest", "year": 2024 }
`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Bulk completed with %d items\n", len(bulkResp.Items))
```

To send bulk requests to ingest nodes without the rest of the default router, build a router from policies. Policies are tried in order, so this one prefers ingest nodes and falls back to any node; drop the second policy to fail when no ingest node is available:

```go
	ingestPolicy, err := opensearchtransport.NewRolePolicy(opensearchtransport.RoleIngest)
	if err != nil {
		return err
	}
	ingestPreferred := opensearchtransport.NewRouter(
		ingestPolicy,
		opensearchtransport.NewRoundRobinPolicy(),
	)
```

[Request Routing](transport-routing.md) describes the routers, the policies and connection scoring in full. `BulkIndexer` goes further than routing by request type: it groups documents by shard and sends each group to the node that hosts the shard's primary. See [Bulk Indexer](indexing-bulk_indexer.md).

## Cleanup

To remove what this guide created, delete the `movies` and `books` indices:

```go
	delResp, err := client.Indices.Delete(
		ctx,
		&opensearchapi.IndicesDeleteReq{
			Indices: []string{"movies", "books"},
			Params:  &opensearchapi.IndicesDeleteParams{IgnoreUnavailable: new(true)},
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Deleted: %t\n", delResp.Acknowledged)

	return nil
}
```

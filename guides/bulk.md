# Bulk

In this guide, you'll learn how to use the OpenSearch Golang Client API to perform bulk operations. You'll learn how to index, update, and delete multiple documents in a single request.

## Setup

First, create a client instance with the following code:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
)

func main() {
	if err := example(); err != nil {
		fmt.Println(fmt.Sprintf("Error: %s", err))
		os.Exit(1)
	}
}

func example() error {
	// Basic client setup
	client, err := opensearchapi.NewDefaultClient()
	if err != nil {
		return err
	}

	ctx := context.Background()
```

### Advanced Setup: Optimized for Bulk Operations

For high-throughput bulk operations, you can configure the client to automatically route requests to appropriate nodes:

```go
	// Advanced client setup with smart routing for mixed workloads
	advancedClient, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{"http://localhost:9200"},

		// Enable node discovery to find all cluster nodes
		DiscoverNodesOnStart:  true,
		DiscoverNodesInterval: 5 * time.Minute,

		// Configure smart routing: bulk operations go to ingest nodes, searches go to data nodes
		Router: opensearchtransport.NewSmartRouter(),
	})
	if err != nil {
		return err
	}

	// This client will automatically route operations to appropriate nodes:
	// - Bulk operations -> ingest nodes
	// - Search operations -> data nodes
	// - Other operations -> round-robin
	_ = advancedClient
```

Next, create an index named `movies` and another named `books` with the default settings:

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

## Bulk API

The `bulk` API action allows you to perform document operations in a single request. The body of the request consist of minimum two objects that contains the bulk operations and the target documents to index, create, update, or delete.

### Indexing multiple documents

The following code creates two documents in the `movies` index and one document in the `books` index:

```go
	bulkResp, err := client.Bulk(
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

Similarly, instead of calling the `create` method for each document, you can use the `bulk` API to create multiple documents in a single request. The following code creates three documents in the `movies` index and one in the `books` index:

```go
	bulkResp, err = client.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Body: strings.NewReader(`{ "create": { "_index": "movies" } }
{ "title": "Beauty and the Beast 2", "year": 2030 }
{ "create": { "_index": "movies", "_id": 1 } }
{ "title": "Beauty and the Beast 3", "year": 2031 }
{ "create": { "_index": "movies", "_id": 2 } }
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

We omit the `_id` for each document and let OpenSearch generate them for us in this example, just like we can with the `create` method.

### Updating multiple documents

```go
	bulkResp, err = client.Bulk(
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

Note that the updated data is specified in the `doc` with a full or partial JSON document, depending on how much of the document you want to update.

### Deleting multiple documents

If the document doesn't exist, OpenSearch doesn't return an error, but instead returns not_found under result. Delete actions don't require documents on the next line

```go
	bulkResp, err = client.Bulk(
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

### Mix and match operations

You can mix and match the different operations in a single request. The following code creates two documents, updates one document, and deletes another document:

```go
	bulkResp, err = client.Bulk(
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

The `bulk` API returns an array of responses for each operation in the request body. Each response contains a `status` field that indicates whether the operation was successful or not. If the operation was successful, the `status` field is set to a `2xx` code. Otherwise, the response contains an error message in the `error` field.

For comprehensive error handling patterns including retry strategies, retryable error classification, and partial failure monitoring, see [Error Handling and Partial Failures](error_handling.md).

The following code shows an example on how to look for errors in the response:

```go
	bulkResp, err = client.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Body: strings.NewReader("{\"delete\":{\"_index\":\"movies\",\"_id\":1}}\n"),
		},
	)
	if err != nil {
		return err
	}
	for _, item := range bulkResp.Items {
		for operation, resp := range item {
			if resp.Status > 299 {
				fmt.Printf("Bulk %s Error: %s\n", operation, resp.Result)
			}
		}
	}
```

## Timeout Configuration

Bulk operations can be long-running, especially when indexing large batches. Two independent timeout mechanisms control how long the operation is allowed to run:

- **Server-side timeout** (`BulkParams.Timeout`): Tells the OpenSearch server to abort the bulk operation if it hasn't completed within the specified duration. This is sent as the `?timeout=` query parameter.
- **Client-side timeout** (`context.Context`): Controls how long the Go HTTP client waits for a response before cancelling the request.

### Setting a server-side timeout

```go
	bulkResp, err := client.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Index: "movies",
			Body: strings.NewReader(`{ "index": { "_id": 1 } }
{ "title": "Beauty and the Beast", "year": 1991 }
{ "index": { "_id": 2 } }
{ "title": "Beauty and the Beast - Live Action", "year": 2017 }
`),
			Params: opensearchapi.BulkParams{
				Timeout: 30 * time.Second,
			},
		},
	)
	if err != nil {
		return err
	}
```

### Layering server-side and client-side timeouts

Set the server-side timeout shorter than the client-side context deadline. This ensures the server aborts the operation before the client gives up, preventing orphaned server-side work that continues consuming resources after the client has disconnected.

```go
	// Client-side deadline: 60s
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Server-side timeout: 45s (shorter than the client deadline)
	bulkResp, err := client.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Index: "movies",
			Body:  body,
			Params: opensearchapi.BulkParams{
				Timeout: 45 * time.Second,
			},
		},
	)
	if err != nil {
		return err
	}
```

If the server-side timeout fires first, the response will contain items with `timeout_exception` errors that can be inspected and retried. If the client-side context expires first, the client receives a context deadline error but the server may still be processing the request -- which is the situation to avoid.

### Why missing server-side timeouts can cause cascading overload

In codebases that perform bulk inserts without setting `BulkParams.Timeout`, a common failure pattern emerges when the client-side context deadline expires:

1. The client cancels the HTTP request and receives `context.DeadlineExceeded`.
2. The server does not observe the cancellation immediately. The bulk operation continues writing documents to shards.
3. The client's retry logic treats the deadline error as transient and resubmits the same batch.
4. The retry typically lands on the same primary shards (routing is deterministic by document ID). The server may now be processing both the original request and the retry concurrently.
5. Subsequent retries can add further concurrent bulk operations to the same shards. As thread pools fill and queues back up, the server begins rejecting requests with `es_rejected_execution_exception`.
6. Rejections from overloaded shards can trigger retries from other clients sharing those shards, widening the impact.

The root cause is that without a server-side timeout, the server has no instruction to abort. The client's context cancellation closes the HTTP connection, but server-side shard operations that have already been dispatched run to completion.

**Always set `BulkParams.Timeout`**. The server-side timeout is the only mechanism that causes the server to abort incomplete shard operations. The client-side context deadline controls how long the _client_ waits, but does not stop server-side work.

#### Use client-assigned document IDs for recoverability

When bulk-inserting documents without explicit `_id` values, OpenSearch auto-generates IDs. If the client-side context expires before the response is received, there is no way to determine which documents were written and which were not. The batch becomes an unknown: some documents may exist on the server with auto-generated IDs that the client never received.

Setting `_id` on each bulk item makes the outcome recoverable. After a timeout or ambiguous failure, the client can query for the expected document IDs to determine which items were persisted and which need to be retried. This turns a blind retry (which risks duplicates or pile-up) into a targeted one.

```go
// With client-assigned IDs, the outcome of a failed bulk request is recoverable
{ "index": { "_index": "events", "_id": "evt-20260226-0001" } }
{ "index": { "_index": "events", "_id": "evt-20260226-0002" } }

// After a timeout, check which documents exist before retrying:
//   GET /events/_mget { "ids": ["evt-20260226-0001", "evt-20260226-0002"] }
```

For retry strategies that account for this failure mode, see [Error Handling and Partial Failures](error_handling.md).

## Performance Optimization for Bulk Operations

### Automatic Ingest Node Routing

For production environments with dedicated ingest nodes, you can optimize bulk operation performance by routing requests to the most appropriate nodes:

```go
	// Create a client optimized for bulk operations
	bulkClient, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{"http://localhost:9200"},

		// Enable node discovery
		DiscoverNodesOnStart:  true,
		DiscoverNodesInterval: 5 * time.Minute,

		// Use smart router for automatic operation routing (recommended)
		Router: opensearchtransport.NewSmartRouter(),
	})
	if err != nil {
		return err
	}

	// This bulk request will automatically route to ingest nodes
	bulkResp, err := bulkClient.Bulk(
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
	fmt.Printf("Optimized bulk completed with %d items\n", len(bulkResp.Items))
```

### Choosing the Right Selector

You can choose different routing strategies based on your cluster setup:

```go
	// Option 1: Prefer ingest nodes, fallback to any available node
	ingestPolicy, _ := opensearchtransport.NewRolePolicy(opensearchtransport.RoleIngest)
	ingestPreferred := opensearchtransport.NewRouter(
		ingestPolicy,
		opensearchtransport.NewRoundRobinPolicy(),
	)

	// Option 2: Only use ingest nodes, fail if none available (strict mode)
	ingestOnly := opensearchtransport.NewRouter(ingestPolicy)

	// Option 3: Automatically detect operation type and route appropriately (recommended)
	// NewSmartRouter() is affinity-aware (currently what NewDefaultRouter() returns).
	// Use NewMuxRouter() for role-based routing without affinity.
	smartRouter := opensearchtransport.NewSmartRouter()

	// Option 4: Custom policy for specific requirements
	// Note: In practice, avoid excluding cluster managers as they're excluded by default
	ingestPolicy, _ = opensearchtransport.NewRolePolicy(opensearchtransport.RoleIngest)
	customRouter := opensearchtransport.NewRouter(
		ingestPolicy,
		opensearchtransport.NewRoundRobinPolicy(),
	)
```

The smart router automatically detects different operation types:

- **Bulk operations** (`/_bulk`) -> Routes to ingest nodes
- **Ingest pipeline operations** (`/_ingest/`) -> Routes to ingest nodes
- **Search operations** (`/_search`) -> Routes to data nodes
- **Other operations** -> Uses default routing

## Cleanup

To clean up the resources created in this guide, delete the `movies` and `books` indices:

```go
	delResp, err := client.Indices.Delete(
		ctx,
		opensearchapi.IndicesDeleteReq{
			Indices: []string{"movies", "books"},
			Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Deleted: %t\n", delResp.Acknowledged)

	return nil
}
```

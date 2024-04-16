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

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

func main() {
	if err := example(); err != nil {
		fmt.Println(fmt.Sprintf("Error: %s", err))
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

Next, create an index named `movies` and another named `books` with the default settings:

```go
	movies := "movies"
	books := "books"

	createResp, err := client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: movies})
	if err != nil {
		return err
	}
	fmt.Printf("Index created: %t\n", createResp.Acknowledged)

	createResp, err := client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: books})
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

If the document doesn’t exist, OpenSearch doesn’t return an error, but instead returns not_found under result. Delete actions don’t require documents on the next line

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

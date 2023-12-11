# Document Lifecycle

This guide covers OpenSearch Golang Client API actions for Document Lifecycle. You'll learn how to create, read, update, and delete documents in your OpenSearch cluster. Whether you're new to OpenSearch or an experienced user, this guide provides the information you need to manage your document lifecycle effectively.

## Setup

Assuming you have OpenSearch running locally on port 9200, you can create a client instance with the following code:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/opensearch-project/opensearch-go/v3/opensearchapi"
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

Next, create an index named `movies` with the default settings:

```go
	createResp, err := client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: "movies"})
	if err != nil {
		return err
	}
	fmt.Printf("Created: %t\n", createResp.Acknowledged)
```

## Document API Actions

### Create a new document with specified ID

To create a new document, use the `create` or `index` API action. The following code creates two new documents with IDs of `1` and `2`:

```go
	docCreateResp, err := client.Document.Create(
		ctx,
		opensearchapi.DocumentCreateReq{
			Index:      "movies",
			DocumentID: "1",
			Body:       strings.NewReader(`{"title": "Beauty and the Beast", "year": 1991 }`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Document: %s\n", docCreateResp.Result)

	docCreateResp, err = client.Document.Create(
		ctx,
		opensearchapi.DocumentCreateReq{
			Index:      "movies",
			DocumentID: "2",
			Body:       strings.NewReader(`{"title": "Beauty and the Beast - Live Action", "year": 2017 }`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Document: %s\n", docCreateResp.Result)
```

Note that the `create` action is NOT idempotent. If you try to create a document with an ID that already exists, the request will fail:

```go
	_, err = client.Document.Create(
		ctx,
		opensearchapi.DocumentCreateReq{
			Index:      "movies",
			DocumentID: "2",
			Body:       strings.NewReader(`{"title": "Just Another Movie" }`),
		},
	)
	if err != nil {
		fmt.Println(err)
	}
```

The `index` action, on the other hand, is idempotent. If you try to index a document with an existing ID, the request will succeed and overwrite the existing document. Note that no new document will be created in this case. You can think of the `index` action as an upsert:

```go
	indexResp, err := client.Index(
		ctx,
		opensearchapi.IndexReq{
			Index:      "movies",
			DocumentID: "2",
			Body:       strings.NewReader(`{"title": "Updated Title" }`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Document: %s\n", indexResp.Result)
```

### Create a new document with auto-generated ID

You can also create a new document with an auto-generated ID by omitting the `id` parameter. The following code creates documents with an auto-generated IDs in the `movies` index:

```go
	indexResp, err = client.Index(
		ctx,
		opensearchapi.IndexReq{
			Index: "movies",
			Body:  strings.NewReader(`{ "title": "The Lion King 2", "year": 1978}`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err := json.MarshalIndent(indexResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Index:\n%s\n", respAsJson)
```

In this case, the ID of the created document in the `result` field of the response body:

```json
{
  "_index": "movies",
  "_id": "jfp7ZYcBPWlPSrIHCMga",
  "_version": 1,
  "result": "created",
  "_shards": {
    "total": 2,
    "successful": 1,
    "failed": 0
  },
  "_seq_no": 4,
  "_primary_term": 1
}
```

### Get a document

To get a document, use the `get` API action. The following code gets the document with ID `1` from the `movies` index:

```go
	getResp, err := client.Document.Get(ctx, opensearchapi.DocumentGetReq{Index: "movies", DocumentID: "1"})
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(getResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get Document:\n%s\n", respAsJson)
```

You can also use `_source_include` and `_source_exclude` parameters to specify which fields to include or exclude in the response:

```go
	getResp, err = client.Document.Get(
		ctx,
		opensearchapi.DocumentGetReq{
			Index:      "movies",
			DocumentID: "1",
			Params:     opensearchapi.DocumentGetParams{SourceIncludes: []string{"title"}},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(getResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get Document:\n%s\n", respAsJson)

	getResp, err = client.Document.Get(
		ctx,
		opensearchapi.DocumentGetReq{
			Index:      "movies",
			DocumentID: "1",
			Params:     opensearchapi.DocumentGetParams{SourceExcludes: []string{"title"}},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(getResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get Document:\n%s\n", respAsJson)
```

### Get multiple documents

To get multiple documents, use the `mget` API action:

```go
	mgetResp, err := client.MGet(
		ctx,
		opensearchapi.MGetReq{
			Index: "movies",
			Body:  strings.NewReader(`{ "docs": [{ "_id": "1" }, { "_id": "2" }] }`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(mgetResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("MGet Document:\n%s\n", respAsJson)
```

### Check if a document exists

To check if a document exists, use the `exists` API action. The following code checks if the document with ID `1` exists in the `movies` index:

```go
	existsResp, err := client.Document.Exists(ctx, opensearchapi.DocumentExistsReq{Index: "movies", DocumentID: "1"})
	if err != nil {
		return err
	}
	fmt.Println(existsResp.Status())
```

### Update a document

To update a document, use the `update` API action. The following code updates the `year` field of the document with ID `1` in the `movies` index:

```go
	updateResp, err := client.Update(
		ctx,
		opensearchapi.UpdateReq{
			Index:      "movies",
			DocumentID: "1",
			Body:       strings.NewReader(`{ "doc": { "year": 1995 } }`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(updateResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Update:\n%s\n", respAsJson)
```

Alternatively, you can use the `script` parameter to update a document using a script. The following code increments the `year` field of the of document with ID `1` by 5 using painless script, the default scripting language in OpenSearch:

```go
	updateResp, err = client.Update(
		ctx,
		opensearchapi.UpdateReq{
			Index:      "movies",
			DocumentID: "1",
			Body:       strings.NewReader(`{ "script": { "source": "ctx._source.year += 5" } }`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(updateResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Update:\n%s\n", respAsJson)
```

Note that while both `update` and `index` actions perform updates, they are not the same. The `update` action is a partial update, while the `index` action is a full update. The `update` action only updates the fields that are specified in the request body, while the `index` action overwrites the entire document with the new document.

### Update multiple documents by query

To update documents that match a query, use the `update_by_query` API action. The following code decreases the `year` field of all documents with `year` greater than 2023:

```go
	_, err = client.Indices.Refresh(ctx, &opensearchapi.IndicesRefreshReq{Indices: []string{"movies"}})
	if err != nil {
		return err
	}

	upByQueryResp, err := client.UpdateByQuery(
		ctx,
		opensearchapi.UpdateByQueryReq{
			Indices: []string{"movies"},
			Params:  opensearchapi.UpdateByQueryParams{Query: "year:<1990"},
			Body:    strings.NewReader(`{"script": { "source": "ctx._source.year -= 1" } }`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(upByQueryResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("UpdateByQuery:\n%s\n", respAsJson)
```

Note that the `update_by_query` API action is needed to refresh the index before the query is executed.

### Delete a document

To delete a document, use the `delete` API action. The following code deletes the document with ID `1`:

```go
	docDelResp, err := client.Document.Delete(ctx, opensearchapi.DocumentDeleteReq{Index: "movies", DocumentID: "1"})
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(docDelResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Del Doc:\n%s\n", respAsJson)
```

### Delete multiple documents by query

To delete documents that match a query, use the `delete_by_query` API action. The following code deletes all documents with `year` greater than 2023:

```go
	_, err = client.Indices.Refresh(ctx, &opensearchapi.IndicesRefreshReq{Indices: []string{"movies"}})
	if err != nil {
		return err
	}

	delByQueryResp, err := client.Document.DeleteByQuery(
		ctx,
		opensearchapi.DocumentDeleteByQueryReq{
			Indices: []string{"movies"},
			Body:    strings.NewReader(`{ "query": { "match": { "title": "The Lion King" } } }`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(delByQueryResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("DelByQuery Doc:\n%s\n", respAsJson)
```

Note that the `delete_by_query` API action is needed to refresh the index before the query is executed.

## Cleanup

To clean up the resources created in this guide, delete the `movies` index:

```go
	delResp, err := client.Indices.Delete(
		ctx,
		opensearchapi.IndicesDeleteReq{
			Indices: []string{"movies"},
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

# Document Lifecycle

This guide covers OpenSearch Golang Client API actions for Document Lifecycle. You'll learn how to create, read, update, and delete documents in your OpenSearch cluster. Whether you're new to OpenSearch or an experienced user, this guide provides the information you need to manage your document lifecycle effectively.

## Setup

Assuming you have OpenSearch running locally on port 9200, you can create a client instance with the following code:

```go
package main

import (
	"github.com/opensearch-project/opensearch-go/v2"
	"log"
)

func main() {
	client, err := opensearch.NewDefaultClient()
	if err != nil {
		log.Printf("error occurred: [%s]", err.Error())
	}
	log.Printf("response: [%+v]", client)
}
```

Next, create an index named `movies` with the default settings:

```go
movies := "movies"

// delete the indexes if they exist
deleteIndexes, err := client.Indices.Delete(
[]string{movies},
client.Indices.Delete.WithIgnoreUnavailable(true),
)
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", deleteIndexes)

createMovieIndex, err := client.Indices.Create(movies)
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", createMovieIndex)
```

## Document API Actions

### Create a new document with specified ID

To create a new document, use the `create` or `index` API action. The following code creates two new documents with IDs of `1` and `2`:

```go
res, err := client.Create(movies, "1", strings.NewReader(`{"title": "Beauty and the Beast", "year": 1991 }`))
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)

res, err = client.Create(movies, "2", strings.NewReader(`{"title": "Beauty and the Beast - Live Action", "year": 2017 }`))
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

Note that the `create` action is NOT idempotent. If you try to create a document with an ID that already exists, the request will fail:

```go
res, err = client.Create(movies, "2", strings.NewReader(`{"title": "Just Another Movie" }`))
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
```

The `index` action, on the other hand, is idempotent. If you try to index a document with an existing ID, the request will succeed and overwrite the existing document. Note that no new document will be created in this case. You can think of the `index` action as an upsert:

```go
res, err = client.Index(movies, strings.NewReader(`{"title": "Updated Title" }`), client.Index.WithDocumentID("2"))
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)

res, err = client.Index(movies, strings.NewReader(`{ "title": "The Lion King", "year": 1994}`), client.Index.WithDocumentID("2"))
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

### Create a new document with auto-generated ID

You can also create a new document with an auto-generated ID by omitting the `id` parameter. The following code creates documents with an auto-generated IDs in the `movies` index:

```go
res, err = client.Index(movies, strings.NewReader(`{ "title": "The Lion King 2", "year": 1978}`))
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
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
res, err = client.Get(movies, "1")
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
// OUTPUT: {"_index":"movies","_id":"1","_version":1,"_seq_no":0,"_primary_term":1,"found":true,"_source":{"title": "Beauty and the Beast", "year": 1991 }}
```

You can also use `_source_include` and `_source_exclude` parameters to specify which fields to include or exclude in the response:

```go
res, err = client.Get(movies, "1", client.Get.WithSourceIncludes("title"))
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
// OUTPUT: {"_index":"movies","_id":"1","_version":1,"_seq_no":0,"_primary_term":1,"found":true,"_source":{"title":"Beauty and the Beast"}}

res, err = client.Get(movies, "1", client.Get.WithSourceExcludes("title"))
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
// OUTPUT: {"_index":"movies","_id":"1","_version":1,"_seq_no":0,"_primary_term":1,"found":true,"_source":{"year":1991}}
```

### Get multiple documents

To get multiple documents, use the `mget` API action:

```go
res, err = client.Mget(strings.NewReader(`{ "docs": [{ "_id": "1" }, { "_id": "2" }] }`), client.Mget.WithIndex(movies))
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
// OUTPUT: {"docs":[{"_index":"movies","_id":"1","_version":1,"_seq_no":0,"_primary_term":1,"found":true,"_source":{"title": "Beauty and the Beast", "year": 1991 }},{"_index":"movies","_id":"2","_version":3,"_seq_no":3,"_primary_term":1,"found":true,"_source":{ "title": "The Lion King", "year": 1994}}]}
```

### Check if a document exists

To check if a document exists, use the `exists` API action. The following code checks if the document with ID `1` exists in the `movies` index:

```go
res, err = client.Exists(movies, "1")
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

### Update a document

To update a document, use the `update` API action. The following code updates the `year` field of the document with ID `1` in the `movies` index:

```go
res, err = client.Update(movies, "1", strings.NewReader(`{ "doc": { "year": 1995 } }`))
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

Alternatively, you can use the `script` parameter to update a document using a script. The following code increments the `year` field of the of document with ID `1` by 5 using painless script, the default scripting language in OpenSearch:

```go
res, err = client.Update(movies, "1", strings.NewReader(`{ "script": { "source": "ctx._source.year += 5" } }`))
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

Note that while both `update` and `index` actions perform updates, they are not the same. The `update` action is a partial update, while the `index` action is a full update. The `update` action only updates the fields that are specified in the request body, while the `index` action overwrites the entire document with the new document.

### Update multiple documents by query

To update documents that match a query, use the `update_by_query` API action. The following code decreases the `year` field of all documents with `year` greater than 2023:

```go
res, err = client.Indices.Refresh(
client.Indices.Refresh.WithIndex(movies),
)
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)

res, err = client.UpdateByQuery(
[]string{movies},
client.UpdateByQuery.WithQuery("year:<1990"),
client.UpdateByQuery.WithBody(
strings.NewReader(`{"script": { "source": "ctx._source.year -= 1" } }`),
),
)
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

Note that the `update_by_query` API action is needed to refresh the index before the query is executed.

### Delete a document

To delete a document, use the `delete` API action. The following code deletes the document with ID `1`:

```go
res, err = client.Delete(movies, "1")
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
}
```

### Delete multiple documents by query

To delete documents that match a query, use the `delete_by_query` API action. The following code deletes all documents with `year` greater than 2023:

```go
res, err = client.Indices.Refresh(
    client.Indices.Refresh.WithIndex(movies),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)

res, err = client.DeleteByQuery(
    []string{movies},
    strings.NewReader(`{ "query": { "match": { "title": "The Lion King" } } }`),
    client.DeleteByQuery.WithQuery(`title: "The Lion King"`),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

Note that the `delete_by_query` API action is needed to refresh the index before the query is executed.

## Cleanup

To clean up the resources created in this guide, delete the `movies` index:

```go
deleteIndexes, err := client.Indices.Delete(
[]string{movies},
client.Indices.Delete.WithIgnoreUnavailable(true),
)
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", deleteIndexes)
```

# Bulk

In this guide, you'll learn how to use the OpenSearch Golang Client API to perform bulk operations. You'll learn how to index, update, and delete multiple documents in a single request.

## Setup

First, create a client instance with the following code:

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

Next, create an index named `movies` and another named `books` with the default settings:

```go
movies := "movies"
books := "books"

createMovieIndex, err := client.Indices.Create(movies)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", createMovieIndex)

createBooksIndex, err := client.Indices.Create(books)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", createBooksIndex)
```

## Bulk API

The `bulk` API action allows you to perform document operations in a single request. The body of the request is an array of objects that contains the bulk operations and the target documents to index, create, update, or delete.

### Indexing multiple documents

The following code creates two documents in the `movies` index and one document in the `books` index:

```go
    res, err := client.Bulk(strings.NewReader(`{ "index": { "_index": "movies", "_id": 1 } }
{ "title": "Beauty and the Beast", "year": 1991 }
{ "index": { "_index": "movies", "_id": 2 } }
{ "title": "Beauty and the Beast - Live Action", "year": 2017 }
{ "index": { "_index": "books", "_id": 1 } }
{ "title": "The Lion King", "year": 1994 }
`))
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", res)
```

### Creating multiple documents

Similarly, instead of calling the `create` method for each document, you can use the `bulk` API to create multiple documents in a single request. The following code creates three documents in the `movies` index and one in the `books` index:

```go
    res, err = client.Bulk(strings.NewReader(`{ "create": { "_index": "movies" } }
{ "title": "Beauty and the Beast 2", "year": 2030 }
{ "create": { "_index": "movies", "_id": 1 } }
{ "title": "Beauty and the Beast 3", "year": 2031 }
{ "create": { "_index": "movies", "_id": 2 } }
{ "title": "Beauty and the Beast 4", "year": 2049 }
{ "create": { "_index": "books" } }
{ "title": "The Lion King 2", "year": 1998 }
`))
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", res)
```

We omit the `_id` for each document and let OpenSearch generate them for us in this example, just like we can with the `create` method.

### Updating multiple documents

```go
    res, err = client.Bulk(strings.NewReader(`{ "update": { "_index": "movies", "_id": 1 } }
{ "doc": { "year": 1992 } }
{ "update": { "_index": "movies", "_id": 1 } }
{ "doc": { "year": 2018 } }
`))
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", res)
```

Note that the updated data is specified in the `doc` with a full or partial JSON document, depending on how much of the document you want to update.

### Deleting multiple documents

If the document doesn’t exist, OpenSearch doesn’t return an error, but instead returns not_found under result. Delete actions don’t require documents on the next line

```go
    res, err = client.Bulk(strings.NewReader(`{ "delete": { "_index": "movies", "_id": 1 } }
    { "delete": { "_index": "movies", "_id": 2 } }
    `))
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", res)
```

### Mix and match operations

You can mix and match the different operations in a single request. The following code creates two documents, updates one document, and deletes another document:

```go
    res, err = client.Bulk(strings.NewReader(`{ "create": { "_index": "movies", "_id": 3 } }
{ "title": "Beauty and the Beast 5", "year": 2050 }
{ "create": { "_index": "movies", "_id": 4 } }
{ "title": "Beauty and the Beast 6", "year": 2051 }
{ "update": { "_index": "movies", "_id": 3 } }
{ "doc": { "year": 2052 } }
{ "delete": { "_index": "movies", "_id": 4 } }
`))
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", res)
```

### Handling errors

The `bulk` API returns an array of responses for each operation in the request body. Each response contains a `status` field that indicates whether the operation was successful or not. If the operation was successful, the `status` field is set to a `2xx` code. Otherwise, the response contains an error message in the `error` field.

The following code shows how to look for errors in the response:

```go
type Response struct {
    Took   int  `json:"took"`
    Errors bool `json:"errors"`
    Items  []struct {
        Delete struct {
        Index   string `json:"_index"`
        Id      string `json:"_id"`
        Version int    `json:"_version"`
        Result  string `json:"result"`
        Shards  struct {
            Total      int `json:"total"`
            Successful int `json:"successful"`
            Failed     int `json:"failed"`
        } `json:"_shards"`
        SeqNo       int `json:"_seq_no"`
        PrimaryTerm int `json:"_primary_term"`
        Status      int `json:"status"`
        } `json:"delete,omitempty"`
    } `json:"items"`
}

    res, err = client.Bulk(strings.NewReader(`{ "delete": { "_index": "movies", "_id": 10 } }
`))
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }

    body, err := io.ReadAll(res.Body)
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }

    var response Response
    if err := json.Unmarshal(body, &response); err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }

    for _, item := range response.Items {
        if item.Delete.Status > 299 {
            log.Printf("error occurred: [%s]", item.Delete.Result)
        } else {
            log.Printf("success: [%s]", item.Delete.Result)
        }
    }
```

## Cleanup

To clean up the resources created in this guide, delete the `movies` and `books` indices:

```go
deleteIndexes, err := client.Indices.Delete(
    []string{movies, books},
    client.Indices.Delete.WithIgnoreUnavailable(true),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", deleteIndexes)
```

# Advanced Index Actions

In this guide, we will look at some advanced index actions that are not covered in the [Index Lifecycle](index_lifecycle.md) guide.

## Setup

Let's create a client instance, and an index named `movies`:

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

movies := "movies"

createMovieIndex, err := client.Indices.Create(movies)
if err != nil {
log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", createMovieIndex)
```

## API Actions

### Clear index cache

You can clear the cache of an index or indices by using the `indices.clear_cache` API action. The following example clears the cache of the `movies` index:

```go
res, err := client.Indices.ClearCache(client.Indices.ClearCache.WithIndex(movies))
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

By default, the `indices.clear_cache` API action clears all types of cache. To clear specific types of cache pass the the `query`, `fielddata`, or `request` parameter to the API action:

```go
res, err := client.Indices.ClearCache(
    client.Indices.ClearCache.WithIndex(movies),
    client.Indices.ClearCache.WithFielddata(true),
    client.Indices.ClearCache.WithRequest(true),
    client.Indices.ClearCache.WithQuery(true),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

### Flush index

Sometimes you might want to flush an index or indices to make sure that all data in the transaction log is persisted to the index. To flush an index or indices use the `indices.flush` API action. The following example flushes the `movies` index:

```go
res, err := client.Indices.Flush(
    client.Indices.Flush.WithIndex(movies),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

### Refresh index

You can refresh an index or indices to make sure that all changes are available for search. To refresh an index or indices use the `indices.refresh` API action:

```go
res, err := client.Indices.Refresh(
    client.Indices.Refresh.WithIndex(movies),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

### Open/Close index

You can close an index to prevent read and write operations on the index. A closed index does not have to maintain certain data structures that an opened index require, reducing the memory and disk space required by the index. The following example closes and reopens the `movies` index:

```go
res, err := client.Indices.Close([]string{movies})
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)

res, err = client.Indices.Open([]string{movies})
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

### Force merge index

You can force merge an index or indices to reduce the number of segments in the index. This can be useful if you have a large number of small segments in the index. Merging segments reduces the memory footprint of the index. Do note that this action is resource intensive and it is only recommended for read-only indices. The following example force merges the `movies` index:

```go
res, err := client.Indices.Forcemerge(
    client.Indices.Forcemerge.WithIndex(movies),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

### Clone index

You can clone an index to create a new index with the same mappings, data, and MOST of the settings. The source index must be in read-only state for cloning. The following example blocks write operations from `movies` index, clones the said index to create a new index named `movies_clone`, then re-enables write:

```go
res, err := client.Indices.AddBlock([]string{movies}, "write")
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)

res, err = client.Indices.Clone(movies, "movies_clone")
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)

res, err = client.Indices.PutSettings(
    strings.NewReader(`{"index":{"blocks":{"write":false}}}`),
    client.Indices.PutSettings.WithIndex(movies),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

### Split index

You can split an index into another index with more primary shards. The source index must be in read-only state for splitting. The following example create the read-only `books` index with 30 routing shards and 5 shards (which is divisible by 30), splits index into `bigger_books` with 10 shards (which is also divisible by 30), then re-enables write:

```go
books := "books"

res, err := client.Indices.Create(books,
    client.Indices.Create.WithBody(
        strings.NewReader(`{
            "settings": {
                "index": {
                    "number_of_shards": 5,
                    "number_of_routing_shards": 30,
                    "blocks": {
                        "write": true
                    }
                }
            }
        }`),
    ),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)

res, err = client.Indices.Split(
    books, "bigger_books",
    client.Indices.Split.WithBody(strings.NewReader(`{"settings":{"index":{"number_of_shards": 10}}}`)))
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)

res, err = client.Indices.PutSettings(
    strings.NewReader(`{"index":{"blocks":{"write":false}}}`),
    client.Indices.PutSettings.WithIndex(books),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

## Cleanup

Let's delete all the indices we created in this guide:

```go
// movies and books are assigned to variables in the previous examples
deleteIndexes, err = client.Indices.Delete([]string{movies, books, "bigger_books", "movies_clone"})
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", deleteIndexes)
```

# Search

OpenSearch provides a powerful search API that allows you to search for documents in an index. The search API supports a number of parameters that allow you to customize the search operation. In this guide, we will explore the search API and its parameters.

# Setup

Let's start by creating an index and adding some documents to it:

```go
package main

import (
    "context"
    "fmt"
    "github.com/opensearch-project/opensearch-go/v2"
    "github.com/opensearch-project/opensearch-go/v2/opensearchapi"
    "log"
    "strings"
)

func main() {
    client, err := opensearch.NewDefaultClient()
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", client)

    movies := "movies"

    // create the index
    createMovieIndex, err := client.Indices.Create(movies)
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", createMovieIndex)

    for i := 1; i < 11; i++ {
        req := opensearchapi.IndexRequest{
            Index:      movies,
            DocumentID: fmt.Sprintf("%d", i),
            Body:       strings.NewReader(fmt.Sprintf(`{"title": "The Dark Knight %d", "director": "Christopher Nolan", "year": %d}`, i, 2008+i)),
        }
        _, err := req.Do(context.Background(), client)
        if err != nil {
            log.Printf("error occurred: [%s]", err.Error())
        }
    }

    req := opensearchapi.IndexRequest{
        Index: movies,
        Body:  strings.NewReader(`{"title": "The Godfather", "director": "Francis Ford Coppola", "year": 1972}`),
    }
    _, err = req.Do(context.Background(), client)
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }

    req = opensearchapi.IndexRequest{
        Index: movies,
        Body:  strings.NewReader(`{"title": "The Shawshank Redemption", "director": "Frank Darabont", "year": 1994}`),
    }
    _, err = req.Do(context.Background(), client)
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }

    // refresh the index to make the documents searchable
    _, err = client.Indices.Refresh(client.Indices.Refresh.WithIndex(movies))
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
}
```

## Search API

### Basic Search

The search API allows you to search for documents in an index. The following example searches for ALL documents in the `movies` index:

```go
res, err := client.Search(
    client.Search.WithIndex(movies),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

You can also search for documents that match a specific query. The following example searches for documents that match the query `dark knight`:

```go
part, err := client.Search(
    client.Search.WithIndex(movies),
    client.Search.WithQuery(`title: "dark knight"`),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", part)
```

OpenSearch query DSL allows you to specify complex queries. Check out the [OpenSearch query DSL documentation](https://opensearch.org/docs/latest/query-dsl/) for more information.

### Basic Pagination

The search API allows you to paginate through the search results. The following example searches for documents that match the query `dark knight`, sorted by `year` in ascending order, and returns the first 2 results after skipping the first 5 results:

```go
sort, err := client.Search(
    client.Search.WithIndex(movies),
    client.Search.WithSize(2),
    client.Search.WithFrom(5),
    client.Search.WithSort("year:desc"),
    client.Search.WithQuery(`title: "dark knight"`),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", sort)
```

### Pagination with scroll

When retrieving large amounts of non-real-time data, you can use the `scroll` parameter to paginate through the search results.

```go
page1, err := client.Search(
    client.Search.WithIndex(movies),
    client.Search.WithSize(2),
    client.Search.WithQuery(`title: "dark knight"`),
    client.Search.WithSort("year:asc"),
    client.Search.WithScroll(time.Minute),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", page1)
```

### Pagination with Point in Time

The scroll example above has one weakness: if the index is updated while you are scrolling through the results, they will be paginated inconsistently. To avoid this, you should use the "Point in Time" feature. The following example demonstrates how to use the `point_in_time` and `pit_id` parameters to paginate through the search results:

```go
// create a point in time
_, pit, err := client.PointInTime.Create(
    client.PointInTime.Create.WithIndex(movies),
    client.PointInTime.Create.WithKeepAlive(time.Minute),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("created pit: [%+v]", pit)

// run a search query with a pit.id
page1, err := client.Search(
    client.Search.WithSize(5),
    client.Search.WithBody(strings.NewReader(fmt.Sprintf(`{ "pit": { "id": "%s", "keep_alive": "1m" } }`, pit.PitID))),
    client.Search.WithSort("year:asc"),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", page1)

// to get the next set of documents, run the same query with the last document’s sort values as the search_after parameter, keeping the same sort and pit.id.
page2, err := client.Search(
    client.Search.WithSize(5),
    client.Search.WithBody(strings.NewReader(fmt.Sprintf(`{ "pit": { "id": "%s", "keep_alive": "1m" }, "search_after": [ "1994" ] }`, pit.PitID))),
    client.Search.WithSort("year:asc"),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", page2)

// to delete the point in time, run the following query
_, delpits, err := client.PointInTime.Delete(client.PointInTime.Delete.WithPitID(pit.PitID))
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("deleted pits: [%+v]", delpits)
```

Note that a point-in-time is associated with an index or a set of index. So, when performing a search with a point-in-time, you DO NOT specify the index in the search.

## Cleanup

```go
deleteIndex, err := client.Indices.Delete([]string{"movies"})
if err != nil {
    log.Printf("Error creating index: %s", err.Error())
}
log.Printf("response: [%+v]", deleteIndex)
```

# Search

OpenSearch provides a powerful search API that allows you to search for documents in an index. The search API supports a number of parameters that allow you to customize the search operation. In this guide, we will explore the search API and its parameters.

# Setup

Let's start by creating an index and adding some documents to it:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
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
	exampleIndex := "movies"

	createResp, err := client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: exampleIndex})
	if err != nil {
		return err
	}
	fmt.Printf("Created: %t\n", createResp.Acknowledged)

	for i := 1; i < 11; i++ {
		_, err = client.Index(
			ctx,
			opensearchapi.IndexReq{
				Index:      exampleIndex,
				DocumentID: strconv.Itoa(i),
				Body:       strings.NewReader(fmt.Sprintf(`{"title": "The Dark Knight %d", "director": "Christopher Nolan", "year": %d}`, i, 2008+i)),
			},
		)
		if err != nil {
			return err
		}
	}

	_, err = client.Index(
		ctx,
		opensearchapi.IndexReq{
			Index: exampleIndex,
			Body:  strings.NewReader(`{"title": "The Godfather", "director": "Francis Ford Coppola", "year": 1972}`),
		},
	)
	if err != nil {
		return err
	}

	_, err = client.Index(
		ctx,
		opensearchapi.IndexReq{
			Index: exampleIndex,
			Body:  strings.NewReader(`{"title": "The Shawshank Redemption", "director": "Frank Darabont", "year": 1994}`),
		},
	)
	if err != nil {
		return err
	}

	_, err = client.Indices.Refresh(ctx, &opensearchapi.IndicesRefreshReq{Indices: []string{exampleIndex}})
	if err != nil {
		return err
	}
```

## Search API

### Basic Search

The search API allows you to search for documents in an index. The following example searches for ALL documents in the `movies` index:

```go
	searchResp, err := client.Search(ctx, &opensearchapi.SearchReq{Indices: []string{exampleIndex}})
	if err != nil {
		return err
	}
	respAsJson, err := json.MarshalIndent(searchResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Search Response:\n%s\n", string(respAsJson))
```

You can also search for documents that match a specific query. The following example searches for documents that match the query `dark knight`:

```go
	searchResp, err = client.Search(
		ctx,
		&opensearchapi.SearchReq{
			Indices: []string{exampleIndex},
			Params:  opensearchapi.SearchParams{Query: `title: "dark knight"`},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(searchResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Search Response:\n%s\n", string(respAsJson))
```

OpenSearch query DSL allows you to specify complex queries. Check out the [OpenSearch query DSL documentation](https://opensearch.org/docs/latest/query-dsl/) for more information.

### Basic Pagination

The search API allows you to paginate through the search results. The following example searches for documents that match the query `dark knight`, sorted by `year` in ascending order, and returns the first 2 results after skipping the first 5 results:

```go
	searchResp, err = client.Search(
		ctx,
		&opensearchapi.SearchReq{
			Indices: []string{exampleIndex},
			Params: opensearchapi.SearchParams{
				Query: `title: "dark knight"`,
				Size:  opensearchapi.ToPointer(2),
				From:  opensearchapi.ToPointer(5),
				Sort:  []string{"year:desc"},
			},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(searchResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Search Response:\n%s\n", string(respAsJson))
```

### Pagination with scroll

When retrieving large amounts of non-real-time data, you can use the `scroll` parameter to paginate through the search results.

```go
	searchResp, err = client.Search(
		ctx,
		&opensearchapi.SearchReq{
			Indices: []string{exampleIndex},
			Params: opensearchapi.SearchParams{
				Query:  `title: "dark knight"`,
				Size:   opensearchapi.ToPointer(2),
				Sort:   []string{"year:desc"},
				Scroll: time.Minute,
			},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(searchResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Search Response:\n%s\n", string(respAsJson))
```

### Pagination with Point in Time

The scroll example above has one weakness: if the index is updated while you are scrolling through the results, they will be paginated inconsistently. To avoid this, you should use the "Point in Time" feature. The following example demonstrates how to use the `point_in_time` and `pit_id` parameters to paginate through the search results:

```go
	pitCreateResp, err := client.PointInTime.Create(
		ctx,
		opensearchapi.PointInTimeCreateReq{
			Indices: []string{exampleIndex},
			Params:  opensearchapi.PointInTimeCreateParams{KeepAlive: time.Minute},
		},
	)
	if err != nil {
		return err
	}

	searchResp, err = client.Search(
		ctx,
		&opensearchapi.SearchReq{
			Body: strings.NewReader(fmt.Sprintf(`{ "pit": { "id": "%s", "keep_alive": "1m" } }`, pitCreateResp.PitID)),
			Params: opensearchapi.SearchParams{
				Size: opensearchapi.ToPointer(5),
				Sort: []string{"year:desc"},
			},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(searchResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Search Response:\n%s\n", string(respAsJson))

	searchResp, err = client.Search(
		ctx,
		&opensearchapi.SearchReq{
			Body: strings.NewReader(fmt.Sprintf(`{ "pit": { "id": "%s", "keep_alive": "1m" }, "search_after": [ "1994" ] }`, pitCreateResp.PitID)),
			Params: opensearchapi.SearchParams{
				Size: opensearchapi.ToPointer(5),
				Sort: []string{"year:desc"},
			},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(searchResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Search Response:\n%s\n", string(respAsJson))

	_, err = client.PointInTime.Delete(ctx, opensearchapi.PointInTimeDeleteReq{PitID: []string{pitCreateResp.PitID}})
	if err != nil {
		return err
	}
```

Note that a point-in-time is associated with an index or a set of index. So, when performing a search with a point-in-time, you DO NOT specify the index in the search.

## Source API

The source API returns the source of the documents with included or excluded fields. The following example returns all fields from document source in the `movies` index:

```go
	sourceResp, err := client.Document.Source(
		ctx,
		opensearchapi.DocumentSourceReq{
			Index:      "movies",
			DocumentID: "1",
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(sourceResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Source Response:\n%s\n", string(respAsJson))
```

To include certain fields in the source response, use `SourceIncludes` or `Source`(this field is deprecated and `SourceIncludes` is recommended to be used instead). To get only required fields:

```go
	sourceResp, err := client.Document.Source(
		ctx,
		opensearchapi.DocumentSourceReq{
			Index:      "movies",
			DocumentID: "1",
			Params: opensearchapi.DocumentSourceParams{
				SourceIncludes: []string{"title"},
			},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(sourceResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Source Response:\n%s\n", string(respAsJson))
```

To exclude certain fields in the source response, use `SourceExcludes` as follows:

```go
	sourceResp, err = client.Document.Source(
		ctx,
		opensearchapi.DocumentSourceReq{
			Index:      "movies",
			DocumentID: "1",
			Params: opensearchapi.DocumentSourceParams{
				SourceExcludes: []string{"title"},
			},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(sourceResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Source Response:\n%s\n", string(respAsJson))
```

## Cleanup

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

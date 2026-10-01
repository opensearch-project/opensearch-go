# Search

> **Runnable example:** [`_samples/usage-search.go`](../_samples/usage-search.go)

> **Note:** Examples in this guide use `opensearchutil.NewJSONReader` for request bodies that contain dynamic values. For static query strings, raw JSON is acceptable. When building bodies from user-supplied values, always use structured serialization. See [Security](config-security.md#request-body-construction) for details.

OpenSearch provides a powerful search API that allows you to search for documents in an index. The search API supports a number of parameters that allow you to customize the search operation. In this guide, we will explore the search API and its parameters.

# Setup

Let's start by creating an index and adding some documents to it:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
	"github.com/opensearch-project/opensearch-go/v5/opensearchutil"
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
	exampleIndex := "movies"
```

### Advanced Setup: Search-Optimized Client

For search-heavy applications, you can configure the client to automatically route search requests to nodes optimized for data retrieval:

```go
	// Advanced client setup optimized for search operations
	router, err := opensearchtransport.NewDefaultRouter()
	if err != nil {
		return err
	}

	searchClient, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{"http://localhost:9200"},

		// Enable node discovery to find all data nodes
		DiscoverNodesOnStart:  new(true),
		DiscoverNodesInterval: 5 * time.Minute,

		// Configure automatic routing to data nodes for search operations
		Router: router,
	})
	if err != nil {
		return err
	}

	// Use search-optimized client for better performance
	_ = searchClient // This client will automatically route searches to data nodes

	createResp, err := client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: exampleIndex})
	if err != nil {
		return err
	}
	fmt.Printf("Created: %t\n", createResp.Acknowledged)

	for i := 1; i < 11; i++ {
		_, err = client.Doc.Index(
			ctx,
			opensearchapi.IndexReq{
				Index: exampleIndex,
				ID:    strconv.Itoa(i),
				Body: opensearchutil.NewJSONReader(map[string]any{
					"title":    fmt.Sprintf("The Dark Knight %d", i),
					"director": "Christopher Nolan",
					"year":     2008 + i,
				}),
			},
		)
		if err != nil {
			return err
		}
	}

	_, err = client.Doc.Index(
		ctx,
		opensearchapi.IndexReq{
			Index: exampleIndex,
			Body:  strings.NewReader(`{"title": "The Godfather", "director": "Francis Ford Coppola", "year": 1972}`),
		},
	)
	if err != nil {
		return err
	}

	_, err = client.Doc.Index(
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

#### Hit counts

`Hits.Total` reports the number of documents matching the query. It is a union rather than a plain integer, because a numeric `track_total_hits` caps the counting effort and the server then reports a lower bound (`"relation": "gte"`) in place of an exact count (`"relation": "eq"`). Unwrap it with `TotalHits()`.

The field is also a pointer, and pointer-typed response fields are conditional on the request that produced them: `track_total_hits` defaults to `true`, so `Hits.Total` is populated for a default search, but a request that sets `SearchParams.TrackTotalHits: "false"` omits the field and a dereference then panics. Requests constructed at the call site that do not disable it may read `Hits.Total` directly, as the examples in this guide do; requests assembled elsewhere, or whose parameters are not known locally, require a nil check.

```go
	if searchResp.Hits.Total == nil {
		return fmt.Errorf("hit count unavailable: the request disabled track_total_hits")
	}
	total, err := searchResp.Hits.Total.TotalHits()
	if err != nil {
		return err
	}
	fmt.Printf("Found %d documents (%s)\n", total.Value, total.Relation)
```

See [`track_total_hits`](https://docs.opensearch.org/latest/api-reference/search-apis/search/) for the parameter and its `eq`/`gte` semantics, and [`SearchHitsMetadataTotal`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchapi#SearchHitsMetadataTotal) for the union's accessors.

You can also search for documents that match a specific query. The following example searches for documents that match the query `dark knight`:

```go
	searchResp, err = client.Search(
		ctx,
		&opensearchapi.SearchReq{
			Indices:  []string{exampleIndex},
			Params: &opensearchapi.SearchParams{Q: `title: "dark knight"`},
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

OpenSearch query DSL allows you to specify complex queries. Check out the [OpenSearch query DSL documentation](https://docs.opensearch.org/latest/query-dsl/) for more information.

### Basic Pagination

The search API allows you to paginate through the search results. The following example searches for documents that match the query `dark knight`, sorted by `year` in ascending order, and returns the first 2 results after skipping the first 5 results:

```go
	searchResp, err = client.Search(
		ctx,
		&opensearchapi.SearchReq{
			Indices: []string{exampleIndex},
			Params: &opensearchapi.SearchParams{
				Q:    `title: "dark knight"`,
				Size: new(2),
				From: 5,
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
```

### Pagination with scroll

When retrieving large amounts of non-real-time data, you can use the `scroll` parameter to paginate through the search results.

```go
	searchResp, err = client.Search(
		ctx,
		&opensearchapi.SearchReq{
			Indices: []string{exampleIndex},
			Params: &opensearchapi.SearchParams{
				Q:      `title: "dark knight"`,
				Size:   new(2),
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

The scroll example above has one weakness: if the index is updated while you are scrolling through the results, they will be paginated inconsistently. A point in time (PIT) fixes that by freezing the indices at the moment it is created. Pair it with `search_after` to page through the results.

The simplest way is `client.PIT.SearchAfter`. It creates a PIT, pages through it with `search_after`, and deletes the PIT when the loop ends, fails, or you break out early. It always opens its own PIT; to page one you already have, such as one from an earlier request, see [Resuming a PIT across requests](#resuming-a-pit-across-requests). Errors come back from the func it returns, which you call after the loop, as with `bufio.Scanner.Err`:

```go
	hits, errf := client.PIT.SearchAfter(
		ctx,
		&opensearchapi.CreatePITReq{Indices: []string{exampleIndex}},
		&opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{
			Sort: &sortByYear, // the last sort key must be unique
			Size: new(5),
		}},
	)
	for hit := range hits {
		fmt.Printf("%s: %s\n", *hit.ID, hit.Source)
	}
	if err := errf(); err != nil {
		return err
	}
```

Where `sortByYear` is:

```go
	sortByYear := opensearchapi.NewSortFromArray([]opensearchapi.SortCombinations{
		opensearchapi.NewSortCombinationsFromFieldSortMap(map[string]opensearchapi.FieldSort{"year": {Order: new("desc")}}),
	})
```

The sort must end in a field that is unique per document, or pages can skip or repeat documents. `year` is unique in this example; in real data, add a unique field such as an event ID as the last sort key. `Size` sets the page size; it must be positive and defaults to 1000.

`client.PIT.SearchAfterPages` does the same but yields whole pages (`*opensearchapi.SearchResp`), for when you need per-page data such as `took` or aggregations.

They never yield an incomplete page, because `search_after` would move past its missing documents for good and the scan would silently lose data. A page with a failed shard stops the scan with a `*opensearchapi.PartialSearchError`, even if the client's error mask hides partial failures, and a page that timed out or terminated early stops it with `opensearchapi.ErrSearchPageIncomplete`. A page whose failed shards were all rejected by a full search queue, or that timed out, is retried twice, after 3 seconds and then 6, before the scan stops, since a PIT returns the same hits each time. The error names the page that stopped the scan and how many hits were yielded before it.

#### Sharing a PIT

To run several searches against the same snapshot, or pass it to other functions, open a PIT handle yourself and close it when you are done. `Close` takes a context; give the deferred close its own, so it still runs after `ctx` is cancelled:

```go
	pit, err := client.PIT.Open(ctx, &opensearchapi.CreatePITReq{Indices: []string{exampleIndex}})
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := pit.Close(closeCtx); err != nil {
			log.Printf("close PIT: %v", err)
		}
	}()

	hits, errf := pit.SearchAfter(ctx, &opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{Sort: &sortByYear}})
	for hit := range hits {
		fmt.Println(*hit.ID)
	}
	if err := errf(); err != nil {
		return err
	}

	resp, err := pit.Search(ctx, &opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{Size: new(1)}})
	if err != nil {
		return err
	}
	fmt.Println(len(resp.Hits.Hits))
```

A PIT handle is safe for concurrent use. Its iterators and `Search` never close it; its owner does. `Close` deletes the PIT within the limits of the context you pass, and the client adds no timeout of its own. It is safe to call more than once, and a failed close can be retried. After a close, `Search` and the iterators return `opensearchapi.ErrPITClosed`.

When you search with a PIT you do not specify indices; the PIT already pins them. `Search` and the iterators reject a request that sets `Indices`.

#### Where a PIT ID goes

A PIT ID has its own type, `opensearchapi.PITID`, and every request and response field that carries one uses it. OpenSearch takes it only in a request body, never in a path, query parameter or header, because the ID is a large base64 token:

| Request                                      | Body field      | Go field                                                                          |
| -------------------------------------------- | --------------- | --------------------------------------------------------------------------------- |
| search                                       | `"pit": {"id"}` | `SearchBody.PIT` (`SearchPointInTimeReference.ID`)                                |
| async search (`plugins/asynchronous_search`) | `"pit": {"id"}` | `AsynchronousSearchSearch.PIT` (`SearchPointInTimeReference.ID`)                  |
| each search of an msearch                    | `"pit": {"id"}` | none: `MSearchReq.Body` is an `io.Reader`, so encode a `SearchBody` for each line |
| delete PIT                                   | `"pit_id": []`  | `DeletePITBody.PITID`                                                             |
| cat PIT segments                             | `"pit_id": []`  | `CatPITSegmentsBody.PITID`                                                        |

It comes back in the create-PIT response (`CreatePITResp.PITID`) and in every search against the PIT (`SearchResp.PITID`). A scroll has its own `scroll_id` and never takes a PIT ID. `PIT.Search`, `PIT.Close` and `SearchCursor.Apply` put the ID in the right place for you.

`PITID` is opaque, so you can't build one from a string literal by accident. To store one, use `id.String()` or encode the whole `SearchCursor` as JSON; to read one back, use `opensearchapi.ParsePITID(s)`, which rejects an empty string. On the wire it is the bare string.

#### Resuming a PIT across requests

A PIT outlives the request that opened it, so you can page through one snapshot over several requests, such as one page per API call. Between requests, keep an `opensearchapi.SearchCursor`: the PIT ID and the last hit's sort values. `resp.Cursor()` reads it from a page, and `cur.Apply(req)` returns the next request with the ID in `Body.PIT` and the sort values in `Body.SearchAfter`. Read that page with `client.SearchAfterPages`. `client.SearchAfter` and `client.SearchAfterPages` page any sorted search, with or without a PIT, with the same checks, retries and incomplete-page guard as the PIT handle.

`nextPage` below serves one page per call. It runs page one without a PIT, so a caller that reads only one page never opens one, and opens the PIT when page two is asked for. If the PIT is gone, it carries on without one. After the last page it deletes the PIT:

```go
// nextPage serves one page and returns the cursor for the next one, or a zero
// cursor after the last page. Page one runs without a PIT, so a caller that
// reads only one page never opens one; the PIT opens when page two is asked for.
func nextPage(
	ctx context.Context, client *opensearchapi.Client, cur opensearchapi.SearchCursor,
) ([]opensearchapi.SearchHit, opensearchapi.SearchCursor, error) {
	const pageSize = 5
	if cur.After != nil && !cur.PIT.IsSet() {
		pit, err := client.PIT.Open(ctx, &opensearchapi.CreatePITReq{
			Indices: []string{exampleIndex},
			Params:  &opensearchapi.CreatePITParams{KeepAlive: 2 * time.Minute},
		})
		if err == nil { // without a PIT, page on over the live index
			cur.PIT = pit.ID()
		}
	}
	req := &opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{Sort: &sortByYear, Size: new(pageSize)}}
	if !cur.PIT.IsSet() {
		req.Indices = []string{exampleIndex}
	}
	page, err := onePage(ctx, client, cur.Apply(req))
	if _, ok := errors.AsType[*opensearchapi.SearchContextMissingError](err); ok {
		// The PIT expired or was deleted: keep the position, lose the snapshot.
		cur.PIT, req.Indices = opensearchapi.PITID{}, []string{exampleIndex}
		page, err = onePage(ctx, client, cur.Apply(req))
	}
	if err != nil {
		return nil, opensearchapi.SearchCursor{}, err
	}
	next, ok := page.Cursor()
	if !ok || len(page.Hits.Hits) < pageSize { // the last page: free the PIT now, not at keep_alive
		if cur.PIT.IsSet() {
			_, err = client.PIT.Delete(ctx, &opensearchapi.DeletePITReq{Body: &opensearchapi.DeletePITBody{PITID: []opensearchapi.PITID{cur.PIT}}})
		}
		return page.Hits.Hits, opensearchapi.SearchCursor{}, err
	}
	return page.Hits.Hits, next, nil
}

// onePage returns the first page of req, with the iterators' checks, retries
// and incomplete-page guard. A scan with no hits returns an empty page.
func onePage(ctx context.Context, client *opensearchapi.Client, req *opensearchapi.SearchReq) (*opensearchapi.SearchResp, error) {
	pages, errf := client.SearchAfterPages(ctx, req)
	for page := range pages {
		return page, nil
	}
	return &opensearchapi.SearchResp{}, errf()
}
```

The server keeps the `keep_alive` set when the PIT was opened and restarts it on every search, so it must cover the longest gap between two page requests. `client.SearchAfter` and `client.SearchAfterPages` never create or delete a PIT, which is why `nextPage` deletes it after the last page; deleting it ends it for everyone holding its ID. With `Body.PIT` set, the request must not set `Indices`. Page one comes from the live index and later pages from the snapshot taken when page two was asked for, so a document indexed or updated in between can be missed, or repeated if its sort value changed.

Every open PIT counts against the cluster's `search.max_open_pit_context` limit, 300 per node by default, and holds segments on disk. With one PIT per paging session, sessions that are abandoned leave their PIT open until `keep_alive` runs out. So keep `keep_alive` as short as the gap between pages allows, open the PIT only when a second page is requested, and fall back to plain `search_after` on a `*SearchContextMissingError` or when a PIT cannot be opened, as `nextPage` does. The fallback keeps the position but not the snapshot, so documents indexed in the meantime can shift the pages.

A PIT ID encodes index names and node IDs. Wrap or encrypt it before you hand it to clients outside your service.

#### Keep-alive

A PIT stays open on the server until `keep_alive` passes without a search against it. The server restarts that timer on every search, including every page of the iterators, so a PIT in use does not expire. `Open` defaults `keep_alive` to 300 seconds; set `CreatePITReq.Params.KeepAlive` to change it. The server caps it at the `point_in_time.max_keep_alive` cluster setting, 24 hours by default, and rejects a longer one.

Closing the PIT, whether with `pit.Close` or through the one-shot iterators, deletes it right away on every normal exit. `keep_alive` is what frees it if your process crashes before it can close it, so keep it only as long as the longest gap between two searches.

The lower-level `client.PIT.Create` and `client.PIT.Delete` calls are still available if you need full control.

## Search Performance Optimization

### Automatic Data Node Routing

For production search workloads, you can optimize performance by ensuring search requests are routed to nodes best suited for data retrieval:

```go
	// Create a search-optimized client
	optimizedSearchClient, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: []string{"http://localhost:9200"},

			// Enable node discovery
			DiscoverNodesOnStart:  new(true),
			DiscoverNodesInterval: 5 * time.Minute,

			// Use data-preferred router for search optimization
			Router: opensearchtransport.NewRouter(
				func() opensearchtransport.Policy {
					policy, _ := opensearchtransport.NewRolePolicy(opensearchtransport.RoleData)
					return policy
				}(),
				opensearchtransport.NewRoundRobinPolicy(),
			),
		},
	})
	if err != nil {
		return err
	}

	// Search requests will automatically route to data nodes
	searchResp, err := optimizedSearchClient.Search(
		ctx,
		&opensearchapi.SearchReq{
			Indices: []string{exampleIndex},
			Params: &opensearchapi.SearchParams{
				Q:    `title: "dark knight"`,
				Size: new(10),
			},
		},
	)
	if err != nil {
		return err
	}
	// Hits.Total is a union; TotalHits() unwraps the {value, relation} form.
	total, err := searchResp.Hits.Total.TotalHits()
	if err != nil {
		return err
	}
	fmt.Printf("Optimized search found %d documents\n", total.Value)
```

### Routing for Mixed Workloads

The router automatically detects operation types and routes them to the most appropriate nodes:

```go
	// Routing: automatically detects search vs ingest operations
	router, err := opensearchtransport.NewDefaultRouter()
	if err != nil {
		return err
	}

	routedClient, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: []string{"http://localhost:9200"},

			DiscoverNodesOnStart:  new(true),
			DiscoverNodesInterval: 5 * time.Minute,

			Router: router,
		},
	})
	if err != nil {
		return err
	}

	// Search operations automatically route to data nodes
	_, err = routedClient.Search(ctx, &opensearchapi.SearchReq{
		Indices: []string{exampleIndex},
	})
	if err != nil {
		return err
	}

	// Multi-search operations also route to data nodes
	_, err = routedClient.MSearch(ctx, &opensearchapi.MSearchReq{
		Body: strings.NewReader(`{}
{"query": {"match_all": {}}}
`),
	})
	if err != nil {
		return err
	}
```

### Routing Strategy Overview

The router provides automatic routing based on the operation being performed:

- **Search operations** (`/_search`, `/_msearch`, document retrieval) -> Data nodes
- **Bulk operations** (`/_bulk`) -> Ingest nodes
- **Ingest operations** (`/_ingest/`) -> Ingest nodes
- **Other operations** -> Default round-robin routing

## Source API

The source API returns the source of the documents with included or excluded fields. The following example returns all fields from document source in the `movies` index:

```go
	sourceResp, err := client.Doc.GetSource(
		ctx,
		opensearchapi.GetSourceReq{
			Index: "movies",
			ID:    "1",
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
	sourceResp, err := client.Doc.GetSource(
		ctx,
		opensearchapi.GetSourceReq{
			Index: "movies",
			ID:    "1",
			Params: &opensearchapi.GetSourceParams{
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
	sourceResp, err = client.Doc.GetSource(
		ctx,
		opensearchapi.GetSourceReq{
			Index: "movies",
			ID:    "1",
			Params: &opensearchapi.GetSourceParams{
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
		&opensearchapi.IndicesDeleteReq{
			Indices:  []string{"movies"},
			Params: &opensearchapi.IndicesDeleteParams{IgnoreUnavailable: new(true)},
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Deleted: %t\n", delResp.Acknowledged)

	return nil
}
```

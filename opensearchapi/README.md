# opensearchapi

Package `opensearchapi` provides a strongly-typed Go client for the OpenSearch REST API. It is generated from the [OpenSearch API specification](https://github.com/opensearch-project/opensearch-api-specification) by `cmd/osgen`.

## Installation

```go
import "github.com/opensearch-project/opensearch-go/v5/opensearchapi"
```

## Client Creation

```go
// From explicit configuration
client, err := opensearchapi.NewClient(opensearchapi.Config{
    Client: opensearch.Config{
        Addresses: []string{"https://localhost:9200"},
        Username:  "admin",
        Password:  "myStrongPassword123!",
    },
})

// Connect to localhost:9200 with default settings
client, err := opensearchapi.NewDefaultClient()
```

To share transport configuration (e.g. with plugin clients), build one `opensearch.Config` and hand it to `NewClient`; the resulting client wraps a single underlying `opensearch.Client`. For the full construction model see the [package overview on pkg.go.dev](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchapi#hdr-Client_Creation), and for the environment-variable overrides see [`guides/config-envvars.md`](../guides/config-envvars.md).

## Making Requests

Every operation follows the same triple pattern: **Req**, **Resp**, **Params**.

```go
// Create an index
_, err := client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
    Index:      "products",
    BodyReader: strings.NewReader(`{"settings":{"number_of_shards":1}}`),
})

// Index a document
_, err = client.Doc.Index(ctx, opensearchapi.IndexReq{
    Index:  "products",
    ID:     "1",
    Body:   strings.NewReader(`{"name":"Widget","price":9.99}`),
    Params: &opensearchapi.IndexParams{Refresh: "true"},
})

// Search
resp, err := client.Search(ctx, &opensearchapi.SearchReq{
    Indices:    []string{"products"},
    BodyReader: strings.NewReader(`{"query":{"match":{"name":"Widget"}}}`),
})

// Hits.Total is a union; TotalHits() unwraps the {value, relation} form. Its
// population is conditional on SearchParams.TrackTotalHits (default true).
if total, err := resp.Hits.Total.TotalHits(); err == nil {
    fmt.Println(total.Value) // 1
}

// Delete the index
_, err = client.Indices.Delete(ctx, &opensearchapi.IndicesDeleteReq{
    Indices: []string{"products"},
})
```

Some response fields are unions rather than plain values. `Hits.Total` is one such field: OpenSearch reports either an exact count or a lower bound, so it is unwrapped through [`TotalHits()`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchapi#SearchHitsMetadataTotal) rather than read directly. Pointer-typed response fields are also conditional on the request; see [the search guide](../guides/usage-search.md) for the cases in which `Hits.Total` is nil.

### Pointer vs value receivers

Operations that have required path parameters accept their Req by value:

```go
client.Doc.Index(ctx, opensearchapi.IndexReq{Index: "my-index", ...})
```

Operations where the entire request is optional accept a pointer (nil-safe):

```go
client.Search(ctx, nil) // searches all indices with default params
```

## Sub-Clients

Operations are grouped into sub-clients that mirror the OpenSearch API namespaces. The table below is a quick-scan cheat sheet; the [package overview on pkg.go.dev](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchapi#hdr-Sub_clients) is the authoritative catalog, with the partition model, alias fields, and name-collision semantics.

| Sub-Client                   | Example Call                                    |
| ---------------------------- | ----------------------------------------------- |
| `client.Cat`                 | `client.Cat.Indices(ctx, nil)`                  |
| `client.Cluster`             | `client.Cluster.Health(ctx, nil)`               |
| `client.Dangling`            | `client.Dangling.DeleteDanglingIndex(ctx, req)` |
| `client.Doc`                 | `client.Doc.Get(ctx, req)`                      |
| `client.Indices`             | `client.Indices.Create(ctx, req)`               |
| `client.Indices.Alias`       | `client.Indices.Alias.Get(ctx, req)`            |
| `client.Indices.Mapping`     | `client.Indices.Mapping.Get(ctx, req)`          |
| `client.Indices.Settings`    | `client.Indices.Settings.Get(ctx, req)`         |
| `client.Nodes`               | `client.Nodes.Stats(ctx, nil)`                  |
| `client.PIT`                 | `client.PIT.Create(ctx, req)`                   |
| `client.Ingest`              | `client.Ingest.GetPipeline(ctx, nil)`           |
| `client.Tasks`               | `client.Tasks.List(ctx, nil)`                   |
| `client.Scroll`              | `client.Scroll.Get(ctx, req)`                   |
| `client.SearchPipeline`      | `client.SearchPipeline.Get(ctx, nil)`           |
| `client.Snapshot`            | `client.Snapshot.Get(ctx, req)`                 |
| `client.Snapshot.Repository` | `client.Snapshot.Repository.Get(ctx, req)`      |

Component-template, index-template, legacy-template, and data-stream operations live on `client.Cluster` and `client.Indices` (e.g. `client.Cluster.GetComponentTemplate`, `client.Indices.GetIndexTemplate`, `client.Indices.GetTemplate`, `client.Indices.GetDataStream`). Script operations (`client.GetScript`, `client.PutScript`) live directly on `client`.

Top-level operations (Search, Reindex, DeleteByQuery, UpdateByQuery, etc.) live directly on `client`. Document operations are canonical on `client.Doc` (with `client.Bulk`, `client.MGet`, and `client.Update` retained as backward-compatible forwarders; `client.Index` is not, since `Index` is the indices sub-client field -- use `client.Doc.Index`); point-in-time operations are on `client.PIT`.

## Response Handling

Every response struct exposes typed fields plus an `Inspect()` method for raw access:

```go
resp, err := client.Search(ctx, &opensearchapi.SearchReq{
    Indices:    []string{"products"},
    BodyReader: strings.NewReader(`{"query":{"match_all":{}}}`),
})
if err != nil {
    log.Fatal(err)
}

// Typed access
for _, hit := range resp.Hits.Hits {
    fmt.Println(string(hit.Source))
}

// Raw HTTP response (status code, headers, body bytes)
raw := resp.Inspect().Response
fmt.Println(raw.StatusCode)
```

### Error handling

On HTTP-level errors (connection failures, timeouts), `err` is non-nil and the response is nil-safe (always returned, never nil). On OpenSearch API errors (4xx/5xx), `err` wraps a parsed error with status and reason:

```go
resp, err := client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: "existing"})
if err != nil {
    // err contains the OpenSearch error reason, e.g.
    // "resource_already_exists_exception: index [existing] already exists"
    fmt.Println(err)
}
```

OpenSearch returns HTTP 200 for many operations that partially succeed (bulk item failures, shard failures on search, replica failures on writes). `opensearchapi` surfaces those as typed Go errors by default. See [Partial Failure Errors](#partial-failure-errors) for the full model.

## Query Parameters

Optional query parameters go in the `Params` struct on each Req:

```go
resp, err := client.Search(ctx, &opensearchapi.SearchReq{
    Indices:    []string{"products"},
    BodyReader: strings.NewReader(`{"query":{"match_all":{}}}`),
    Params: &opensearchapi.SearchParams{
        Size:           opensearch.ToPointer(20),
        From:           40,
        Timeout:        5 * time.Second,
        TrackTotalHits: "true",
        SourceIncludes: []string{"name", "price"},
    },
})
```

Duration parameters (timeouts, intervals) accept `time.Duration` and are formatted automatically. Boolean and enum parameters use their Go-native types.

### Pointer helpers

Some parameters are optional pointers. Use `opensearch.ToPointer` to set them inline:

```go
params := opensearchapi.SomeParams{
    WaitForActiveShards: opensearch.ToPointer("all"),
}
```

`ToPointer` is deprecated. Once the module's go directive reaches Go 1.26, callers can drop it in favor of the native `new(value)` literal form (e.g. `new("all")`).

## Partial Failure Errors

OpenSearch returns HTTP 200 even when a request only partially succeeded: bulk operations whose items failed individually, searches that lost some shards, writes whose replica shards rejected the request. `opensearchapi` turns those partial failures into typed Go errors so they surface through the idiomatic `if err != nil` path.

By default (`Config.Errors == nil` resolves to `errmask.Empty`) every category is reported; set `Config.Errors: errmask.New(errmask.All)` or `OPENSEARCH_GO_ERROR_MASK` to mask categories. Dispatch on the typed errors with a `for`/`switch` over `opensearchapi.Errors(err)`:

```go
resp, err := client.MSearch(ctx, req)
for _, sub := range opensearchapi.Errors(err) {
    switch e := sub.(type) {
    case *opensearchapi.PartialSearchError:
        log.Printf("%d/%d shards failed", e.FailedShards, e.TotalShards)
    case *opensearchapi.MultiSearchItemError:
        log.Printf("%d sub-queries failed", len(e.Items))
    default:
        // transport / HTTP / decoding error
        return err
    }
}
// resp is fully populated; use it regardless of partial failure.
```

[`guides/usage-error_handling.md`](../guides/usage-error_handling.md) is the canonical reference for the full model: the error-mask configuration and env-var override, the [error type reference table](../guides/usage-error_handling.md#error-type-reference), the recommended `for`/`switch` pattern, the `IsPartialFailure`/`ToleratePartialFailures`/`RequireSuccessRate` helpers, and why a type switch is preferred over `errors.As`/`Has` or per-Resp helpers. The exhaustive `OPENSEARCH_GO_ERROR_MASK` token list lives in [`guides/config-envvars.md`](../guides/config-envvars.md#error-masking).

### Operation constants for `ShardFailureError.Operation`

`*ShardFailureError` (returned by `Index`, `Doc.Create`, `Doc.Delete`, `Update`) carries an `Operation` field whose value is one of:

```go
opensearchapi.OperationIndex   // "index"
opensearchapi.OperationCreate  // "create"
opensearchapi.OperationUpdate  // "update"
opensearchapi.OperationDelete  // "delete"
```

## Default Router Injection

`opensearchapi.NewClient` (and `NewDefaultClient`) inject [`opensearchtransport.NewDefaultRouter`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#NewDefaultRouter) when the caller leaves `Config.Client.Router` nil, so requests are routed by node role by default. Set `Config.Client.Router` to supply your own, or `OPENSEARCH_GO_ROUTER=false` to opt out.

See [`guides/config-envvars.md` Default router injection](../guides/config-envvars.md#default-router-injection) for the `OPENSEARCH_GO_ROUTER` behavior table and the `DiscoverNodesOnStart` interaction, and [`guides/transport-routing.md`](../guides/transport-routing.md) for the routing model (role awareness, AIMD, shard-cost weighting).

## Plugins

Plugin APIs (k-NN, ML, Security, ISM, etc.) live in separate top-level packages under [`plugins/`](../plugins/) (`github.com/opensearch-project/opensearch-go/v5/plugins/<name>`). They share the same `opensearch.Client` transport but have independent type hierarchies.

See [plugins/README.md](../plugins/README.md) for usage details and available plugins.

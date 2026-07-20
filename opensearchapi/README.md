# opensearchapi

Package `opensearchapi` provides a strongly-typed Go client for the OpenSearch REST API. It is generated from the [OpenSearch API specification](https://github.com/opensearch-project/opensearch-api-specification) by `cmd/osgen`.

## Installation

```go
import "github.com/opensearch-project/opensearch-go/v5/opensearchapi"
```

## Client Creation

Two constructors cover the common scenarios:

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

To share transport configuration (e.g. with plugin clients), build one `opensearch.Config` and hand it to `NewClient`; the resulting client wraps a single underlying `opensearch.Client`.

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
    Index:      []string{"products"},
    BodyReader: strings.NewReader(`{"query":{"match":{"name":"Widget"}}}`),
})
fmt.Println(resp.Hits.Total.Value) // 1

// Delete the index
_, err = client.Indices.Delete(ctx, &opensearchapi.IndicesDeleteReq{
    Index: []string{"products"},
})
```

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

Operations are grouped into sub-clients that mirror the OpenSearch API namespaces. The table
below is a quick-scan cheat sheet; the [package overview on pkg.go.dev](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchapi#hdr-Sub_clients)
is the authoritative catalog, with the partition model, alias fields, and name-collision
semantics.

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
    Index:      []string{"products"},
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
    Index:      []string{"products"},
    BodyReader: strings.NewReader(`{"query":{"match_all":{}}}`),
    Params: &opensearchapi.SearchParams{
        Size:            20,
        From:            40,
        Timeout:         5 * time.Second,
        TrackTotalHits:  "true",
        SourceIncludes:  []string{"name", "price"},
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

By default (`Config.Errors == nil` resolves to `errmask.Empty`) every category is reported; set `Config.Errors: errmask.New(errmask.All)` or `OPENSEARCH_GO_ERROR_MASK` to mask categories. Dispatch on the typed errors with a `for`/`switch` over `opensearchapi.Errors(err)`.

[`guides/usage-error_handling.md`](../guides/usage-error_handling.md) is the canonical reference for the full model: the error-mask configuration and env-var override, the [error type reference table](../guides/usage-error_handling.md#error-type-reference), the recommended `for`/`switch` pattern, the `IsPartialFailure`/`ToleratePartialFailures`/`RequireSuccessRate` helpers, and why a type switch is preferred over `errors.As`/`Has` or per-Resp helpers. The exhaustive `OPENSEARCH_GO_ERROR_MASK` token list lives in [`guides/config-envvars.md`](../guides/config-envvars.md#opensearch_go_error_mask-tokens).

### Operation constants for `ShardFailureError.Operation`

`*ShardFailureError` (returned by `Index`, `Doc.Create`, `Doc.Delete`, `Update`) carries an `Operation` field whose value is one of:

```go
opensearchapi.OperationIndex   // "index"
opensearchapi.OperationCreate  // "create"
opensearchapi.OperationUpdate  // "update"
opensearchapi.OperationDelete  // "delete"
```

## Default Router Injection

`opensearchapi.NewClient` (and `NewDefaultClient`) inject [`opensearchtransport.NewDefaultRouter`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#NewDefaultRouter) when the caller leaves `config.Client.Router` nil, so requests are routed by node role by default. Set `Config.Client.Router` to supply your own, or `OPENSEARCH_GO_ROUTER=false` to opt out. See [`../guides/transport-routing.md`](../guides/transport-routing.md) for the routing model.

The `OPENSEARCH_GO_ROUTER` environment variable acts as an opt-out:

| `OPENSEARCH_GO_ROUTER` | Behavior                                                |
| ---------------------- | ------------------------------------------------------- |
| unset                  | default Router injected, auto-discovery on              |
| `true` / `1`           | default Router injected, auto-discovery on              |
| `false` / `0`          | injection skipped (Router stays nil), no auto-discovery |
| unparseable            | default Router injected, auto-discovery on              |

```go
// Default: NewDefaultRouter is injected.
client, _ := opensearchapi.NewClient(opensearchapi.Config{
    Client: opensearch.Config{Addresses: addrs}, // Router == nil
})

// Caller-provided Router is preserved.
custom := opensearchtransport.NewMuxRouter()
client, _ = opensearchapi.NewClient(opensearchapi.Config{
    Client: opensearch.Config{Addresses: addrs, Router: custom},
})
```

A caller-supplied `DiscoverNodesOnStart` value always wins over the env-var-driven side-effect: setting `DiscoverNodesOnStart: &false` keeps auto-discovery off even when `OPENSEARCH_GO_ROUTER=true`.

For routing semantics (role awareness, AIMD, shard-cost weighting) see [`../guides/transport-routing.md`](../guides/transport-routing.md). For node discovery see [`../guides/transport-node_discovery_and_roles.md`](../guides/transport-node_discovery_and_roles.md).

## Plugins

Plugin APIs (k-NN, ML, Security, ISM, etc.) live in separate top-level packages under [`plugins/`](../plugins/) (`github.com/opensearch-project/opensearch-go/v5/plugins/<name>`). They share the same `opensearch.Client` transport but have independent type hierarchies.

See [plugins/README.md](../plugins/README.md) for usage details and available plugins.

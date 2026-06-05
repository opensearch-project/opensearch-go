# opensearchapi (v5 preview)

Package `opensearchapi` (v5 preview) provides a strongly-typed Go client for the OpenSearch REST API. It is generated from the [OpenSearch API specification](https://github.com/opensearch-project/opensearch-api-specification) by `cmd/osgen`.

## Installation

```go
import "github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi"
```

## Client Creation

Two constructors cover the common scenarios:

```go
// From explicit configuration
client, err := opensearchapi.NewClient(opensearchapi.Config{
    Client: opensearch.Config{
        Addresses: []string{"https://localhost:9200"},
        Username:  "admin",
        Password:  "admin",
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
    Index: "products",
    Body:  strings.NewReader(`{"settings":{"number_of_shards":1}}`),
})

// Index a document
_, err = client.Index(ctx, opensearchapi.IndexReq{
    Index:  "products",
    ID:     "1",
    Body:   strings.NewReader(`{"name":"Widget","price":9.99}`),
    Params: &opensearchapi.IndexParams{Refresh: "true"},
})

// Search
resp, err := client.Search(ctx, &opensearchapi.SearchReq{
    Index: []string{"products"},
    Body:  strings.NewReader(`{"query":{"match":{"name":"Widget"}}}`),
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
client.Index(ctx, opensearchapi.IndexReq{Index: "my-index", ...})
```

Operations where the entire request is optional accept a pointer (nil-safe):

```go
client.Search(ctx, nil) // searches all indices with default params
```

## Sub-Clients

Operations are grouped into sub-clients that mirror the OpenSearch API namespaces:

| Sub-Client                   | Example Call                                    |
| ---------------------------- | ----------------------------------------------- |
| `client.Cat`                 | `client.Cat.Indices(ctx, nil)`                  |
| `client.Cluster`             | `client.Cluster.Health(ctx, nil)`               |
| `client.Dangling`            | `client.Dangling.DeleteDanglingIndex(ctx, req)` |
| `client.Document`            | `client.Document.Get(ctx, req)`                 |
| `client.Indices`             | `client.Indices.Create(ctx, req)`               |
| `client.Indices.Alias`       | `client.Indices.Alias.Get(ctx, req)`            |
| `client.Indices.Mapping`     | `client.Indices.Mapping.Get(ctx, req)`          |
| `client.Indices.Settings`    | `client.Indices.Settings.Get(ctx, req)`         |
| `client.Nodes`               | `client.Nodes.Stats(ctx, nil)`                  |
| `client.Script`              | `client.Script.Get(ctx, req)`                   |
| `client.ComponentTemplate`   | `client.ComponentTemplate.Get(ctx, req)`        |
| `client.IndexTemplate`       | `client.IndexTemplate.Get(ctx, req)`            |
| `client.Template`            | `client.Template.Get(ctx, req)`                 |
| `client.DataStream`          | `client.DataStream.Get(ctx, nil)`               |
| `client.PointInTime`         | `client.PointInTime.Create(ctx, req)`           |
| `client.Ingest`              | `client.Ingest.GetPipeline(ctx, nil)`           |
| `client.Tasks`               | `client.Tasks.List(ctx, nil)`                   |
| `client.Scroll`              | `client.Scroll.Get(ctx, req)`                   |
| `client.SearchPipeline`      | `client.SearchPipeline.Get(ctx, nil)`           |
| `client.Snapshot`            | `client.Snapshot.Get(ctx, req)`                 |
| `client.Snapshot.Repository` | `client.Snapshot.Repository.Get(ctx, req)`      |

Top-level operations (Search, Index, Bulk, etc.) live directly on `client`.

## Response Handling

Every response struct exposes typed fields plus an `Inspect()` method for raw access:

```go
resp, err := client.Search(ctx, &opensearchapi.SearchReq{
    Index: []string{"products"},
    Body:  strings.NewReader(`{"query":{"match_all":{}}}`),
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

OpenSearch returns HTTP 200 for many operations that partially succeed (bulk item failures, shard failures on search, replica failures on writes). v5preview surfaces those as typed Go errors by default. See [Partial Failure Errors](#partial-failure-errors) for the full model.

## Query Parameters

Optional query parameters go in the `Params` struct on each Req:

```go
resp, err := client.Search(ctx, &opensearchapi.SearchReq{
    Index: []string{"products"},
    Body:  strings.NewReader(`{"query":{"match_all":{}}}`),
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

`ToPointer` is deprecated and will be removed in v5. Once the module's go directive moves to Go 1.26, callers can drop it entirely in favor of the native `new(value)` literal form (e.g. `new("all")`).

## Partial Failure Errors

OpenSearch returns HTTP 200 even when a request only partially succeeded: bulk operations whose items failed individually, searches that lost some shards, writes whose replica shards rejected the request. v5preview turns those partial failures into typed Go errors so they surface through the idiomatic `if err != nil` path.

### Default behavior

`Config.Errors` is a `*errmask.ErrorMask` pointer. A set bit suppresses (masks) that category; an unset bit reports it.

| Value                               | Meaning                                                       |
| ----------------------------------- | ------------------------------------------------------------- |
| `nil` (default)                     | Use v5preview's default: `errmask.Empty` -- report everything |
| `errmask.New()`                     | Report every category                                         |
| `errmask.New(errmask.All)`          | Mask everything (mimics the v4 default)                       |
| `errmask.New(errmask.SearchShards)` | Mask only that category                                       |

`errmask.None` and `errmask.Unknown` are aliases for `errmask.Empty`. The named values are constants and are not addressable, so build the `*errmask.ErrorMask` with `errmask.New(...)`.

```go
import "github.com/opensearch-project/opensearch-go/v4/errmask"

client, err := opensearchapi.NewClient(opensearchapi.Config{
    Client: opensearch.Config{Addresses: []string{"https://localhost:9200"}},
    Errors: errmask.New(errmask.SearchShards), // mask only SearchShards
})
```

### Environment-variable override

`OPENSEARCH_GO_ERROR_MASK` accepts a comma-separated list of `+`/`-` tokens applied left-to-right on top of `Config.Errors`. Tokens are the lowercase snake_case form of an error category name -- `bulk_items` for the `BulkItems` bit, `search_shards` for `SearchShards`, `write_shards` for `WriteShards`, `multi_search_items` for `MultiSearchItems`, and so on. The full list of categories appears in the [Error type reference](#error-type-reference) below.

```sh
# Mask everything except bulk-item errors
export OPENSEARCH_GO_ERROR_MASK="+all,-bulk_items"

# Only mask search-shard failures; report every other category
export OPENSEARCH_GO_ERROR_MASK="search_shards"

# Mask everything (mimics the v4 default)
export OPENSEARCH_GO_ERROR_MASK="all"

# Report everything (the v5preview default)
export OPENSEARCH_GO_ERROR_MASK="none"
```

Unknown tokens are silently dropped (forward-compatible) and reported via the debug logger when `OPENSEARCH_GO_DEBUG=true`.

### Inspecting errors with `opensearchapi.Errors`

Operations that can return more than one category of partial failure on the same response (today: `MSearch`, `MSearchTemplate`) sometimes do. The dispatch handler applies a runtime-collapse rule:

- 0 sub-errors fired: returns `nil`.
- 1 sub-error fired: returns the bare sub-error.
- 2+ sub-errors fired: returns a per-op container (e.g. `*MSearchErrors`) implementing `Unwrap() []error`.

`opensearchapi.Errors(err) []error` flattens both shapes into a uniform slice, so a single switch handles every case:

```go
resp, err := client.MSearch(ctx, req)
for _, sub := range opensearchapi.Errors(err) {
    switch e := sub.(type) {
    case *opensearchapi.PartialSearchError:
        log.Printf("shard agg: %d/%d shards failed", e.FailedShards, e.TotalShards)
    case *opensearchapi.MultiSearchItemError:
        log.Printf("%d sub-queries failed", len(e.Items))
    default:
        return err // transport / HTTP / decoding error
    }
}
// resp is fully populated even on partial failure -- continue using it.
```

`opensearchapi.Errors(nil)` returns `nil`. A non-partial `err` (transport, HTTP, decode) returns a single-element slice containing `err`. New wrapper categories added later are picked up by adding a `case`; the `default` keeps existing call sites safe.

### Error type reference

| Error Type               | Returned By                                                                                  | Key Fields                                                             |
| ------------------------ | -------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------- |
| `*PartialBulkError`      | `Bulk`, `BulkStream`                                                                         | `FailedItems []BulkRespItem`, `SucceededCount int`                     |
| `*PartialSearchError`    | `Search`, `MSearch`, `MSearchTemplate`, `SearchTemplate`, `Scroll.Get`, `CreatePIT`, `Count` | `FailedShards int`, `TotalShards int`, `Failures []ShardSearchFailure` |
| `*ShardFailureError`     | `Index`, `Document.Create`, `Document.Delete`, `Update`                                      | `Operation string`, `FailedShards int`, `TotalShards int`              |
| `*MultiSearchItemError`  | `MSearch`, `MSearchTemplate` (per-sub-response error inspection)                             | `Items []MultiSearchItemFailure`, `SucceededCount int`                 |
| `*MSearchErrors`         | `MSearch` when 2+ wrappers fire                                                              | `Unwrap() []error` (multi-error contract)                              |
| `*MSearchTemplateErrors` | `MSearchTemplate` when 2+ wrappers fire                                                      | `Unwrap() []error`                                                     |

All single-bit error types implement the `PartialFailureError` interface and work with `errors.As`. Per-op multi-error containers (`*MSearchErrors`, ...) implement `Unwrap() []error`, so `errors.As` against any sub-error type still matches whether the response carried one or many.

### Recommended pattern

Two patterns cover every partial-failure use case. Pick the one that matches your operation's tolerance:

**Treat any server or API failure as a hard error** -- the simplest and most idiomatic Go path. Use this when the operation has no meaningful "partial success" -- any error is a reason to stop:

```go
resp, err := client.Bulk(ctx, req)
if err != nil {
    return err
}
// resp is fully populated; partial failures (if any) are folded into err.
```

**Inspect categories with a `for`/`switch`** -- when partial error handling is appropriate. Partial error handling lets the client and its application recover from known failure modes they can tolerate (e.g. continue serving a search with a few failed shards, or retry only the bulk items that the server rejected) instead of failing the whole operation. The `default` arm catches transport / HTTP / decode errors and any partial-failure category added in a future release:

```go
resp, err := client.MSearch(ctx, req)
for _, sub := range opensearchapi.Errors(err) {
    switch e := sub.(type) {
    case *opensearchapi.PartialSearchError:
        log.Printf("%d/%d shards failed", e.FailedShards, e.TotalShards)
    case *opensearchapi.MultiSearchItemError:
        log.Printf("%d sub-queries failed", len(e.Items))
    default:
        return err
    }
}
// resp is fully populated; use it regardless of partial failure.
```

`opensearchapi.Errors(err)` flattens every error shape into a uniform slice -- single sub-error, multi-wrapper container, transport error, or `nil` (returns `nil`). The switch is the only pattern this guide recommends for category-aware handling: it stays correct when the API adds new categories, and a missing `case` is reviewable / lint-able.

### Helper functions

```go
// Test whether an error is a partial failure (any type).
if opensearchapi.IsPartialFailure(err) { /* ... */ }

// Suppress all partial failures (best-effort operations).
err = opensearchapi.ToleratePartialFailures(err)

// Threshold-based tolerance: nil unless success rate drops below 99%.
err = opensearchapi.RequireSuccessRate(err, 0.99)
```

### Operation constants for `ShardFailureError.Operation`

```go
opensearchapi.OperationIndex   // "index"
opensearchapi.OperationCreate  // "create"
opensearchapi.OperationUpdate  // "update"
opensearchapi.OperationDelete  // "delete"
```

### Why a type switch, not `errors.As`, `Has`, or per-Resp helpers

The set of partial-failure categories grows as the OpenSearch API evolves: a future server release can add a category today's call sites have never seen. A type switch over `opensearchapi.Errors(err)` makes that growth visible -- review and static analysis can grep for the switch and flag missing cases, and the `default` arm keeps existing call sites safe in the meantime.

`errors.As` and `Has`-style helpers and per-Resp helper methods (`resp.BulkItemFailures()`, `resp.SearchShardFailures()`, ...) all answer the same narrow question: "did _this_ category happen?" None of them can tell a call site that a _new_ category appeared and is being silently dropped. Treat them as an antipattern. The per-Resp helpers exist on the response types as engine machinery for the dispatch and remain available for focused inspection of a known category, but new code should use the type switch.

For the full best-practices guide (retry strategies, threshold tuning, manual partial-failure inspection), see [`../../guides/error_handling.md`](../../guides/error_handling.md).

## Default Router Injection

`v5preview/opensearchapi.NewClient` (and `NewDefaultClient`) inject [`opensearchtransport.NewDefaultRouter`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v4/opensearchtransport#NewDefaultRouter) when the caller leaves `config.Client.Router` nil. v5preview opts every client into intelligent request routing -- role-aware dispatch with RTT-based scoring, congestion-window AIMD, and shard-cost weighting -- by default.

The `OPENSEARCH_GO_ROUTER` environment variable acts as an opt-out:

| `OPENSEARCH_GO_ROUTER` | v5preview behavior                                      |
| ---------------------- | ------------------------------------------------------- |
| unset                  | default Router injected, no auto-discovery              |
| `true` / `1`           | default Router injected, auto-discovery on              |
| `false` / `0`          | injection skipped (Router stays nil), no auto-discovery |
| unparseable            | default Router injected, no auto-discovery              |

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

For routing semantics (role awareness, AIMD, shard-cost weighting) see [`../../guides/routing.md`](../../guides/routing.md). For node discovery see [`../../guides/node_discovery_and_roles.md`](../../guides/node_discovery_and_roles.md).

## Plugins

Plugin APIs (k-NN, ML, Security, ISM, etc.) live in separate packages under `v5preview/opensearchapi/plugins/`. They share the same `opensearch.Client` transport but have independent type hierarchies.

See [plugins/README.md](plugins/README.md) for usage details and available plugins.

## Migrating from v4

Existing v4 `opensearchapi/` callers moving to v5preview need a handful of code changes (import path, `Indices` -> `Index` rename, optional `Params` becomes `*Params`, optional `bool` query params become `*bool`). These renames are a one-time cost: `opensearchapi` is switching from hand-written types to a code-generated client sourced from the [OpenSearch API specification](https://github.com/opensearch-project/opensearch-api-specification), so subsequent spec evolutions arrive as additive types rather than coordinated rename pulls. See [`MIGRATING.md`](MIGRATING.md) for the full delta and a quick checklist.

## Status

`v5preview/opensearchapi/` is a preview of the v5 API surface, shipped inside the v4 module. Types, field names, and method shapes may change before v5 ships. Track [issue #835](https://github.com/opensearch-project/opensearch-go/issues/835) for breakage notices.

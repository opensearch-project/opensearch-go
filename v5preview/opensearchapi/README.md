# opensearchapi (v5 preview)

Package `opensearchapi` (v5 preview) provides a strongly-typed Go client for the OpenSearch REST API. It is generated from the official OpenSearch OpenAPI specification by `cmd/osgen`.

## Installation

```go
import "github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi"
```

## Client Creation

Three constructors cover the common scenarios:

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

// Wrap an existing opensearch.Client (e.g. one shared with plugins)
root, _ := opensearch.NewClient(opensearch.Config{...})
client := opensearchapi.NewFromClient(root)
```

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

## Plugins

Plugin APIs (k-NN, ML, Security, ISM, etc.) live in separate packages under `v5preview/opensearchapi/plugins/`. They share the same `opensearch.Client` transport but have independent type hierarchies.

See [plugins/README.md](plugins/README.md) for usage details and available plugins.

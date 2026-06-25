# Migrating from v4 `opensearchapi/` to v5 `opensearchapi/`

This guide enumerates every code change a v4 caller needs to make to move to the v5 API surface. The package name is identical (`opensearchapi`); the import path changes from `/v4` to `/v5`, so most call sites only need the new import plus a handful of surface tweaks documented below.

For runtime semantics (partial-failure errors, default Router) see [`README.md`](README.md). For the version-history rationale see [`../UPGRADING.md`](../UPGRADING.md). For best-practices guidance see [`../guides/usage-error_handling.md`](../guides/usage-error_handling.md).

## Status

In v5 the `opensearchapi` package is code-generated from the [OpenSearch API specification](https://github.com/opensearch-project/opensearch-api-specification), replacing the hand-written v4 package. The same surface shipped inside the v4 module at `v5preview/opensearchapi/` for early adopters; v5 promotes it to the module root.

## A one-time conversion -- and why

The renames in this guide (`DocumentID` -> `ID`, optional `Params` becoming `*Params`, partial-failure type renames) are unfortunate but unavoidable: this is the one-time cost of switching `opensearchapi` from hand-written types to a code-generated client sourced from the [OpenSearch API specification](https://github.com/opensearch-project/opensearch-api-specification).

We're sorry for the churn. The trade is that future spec evolutions arrive as additive types and methods rather than coordinated rename pulls, the surface stays in lockstep with the server, and bug fixes flow through the spec instead of being re-translated by hand. After this conversion, you should not see another wave of renames at this scale.

## Import path

```go
// v4
import "github.com/opensearch-project/opensearch-go/v4/opensearchapi"

// v5
import "github.com/opensearch-project/opensearch-go/v5/opensearchapi"
```

The package qualifier (`opensearchapi.X`) does not change. The only edit per file is replacing `v4` with `v5` in the import path.

## Field renames you'll hit

### Typed body vs. raw reader

Most body-bearing operations now expose a typed `Body` field (e.g. `*SearchBody`) alongside a `BodyReader io.Reader` escape hatch: build a typed body with `Body`, or pass a raw `io.Reader` via `BodyReader`.

Within `opensearchapi/`, the bulk, NDJSON, and single-document write operations are the exception: `BulkReq`, `BulkStreamReq`, `MSearchReq`, `MSearchTemplateReq`, `CreateReq`, `IndexReq`, and `IndicesCreateDataStreamReq` keep a single raw `Body io.Reader` and have no `BodyReader` field. The generated plugin packages under `plugins/` use the same typed-`Body`/`BodyReader` split, with their own per-package set of raw-body operations.

### Optional `Params` becomes `*Params`

```go
// v4
client.Search(ctx, &opensearchapi.SearchReq{
    Indices: []string{"products"},
    Params:  opensearchapi.SearchParams{Size: 20},
})

// v5
client.Search(ctx, &opensearchapi.SearchReq{
    Indices: []string{"products"},
    Params:  &opensearchapi.SearchParams{Size: 20},
})
```

Pointer-typed `Params` lets callers pass `nil` when no parameters are needed and keeps the struct cheap to copy.

### Shared parameters move into embedded structs

Common query parameters are now grouped into embedded structs (`TimeoutParams`, `DebugParams`) shared across every operation. Fields like `Timeout`, `Pretty`, `Human`, and `ErrorTrace` are set through the embedded struct:

```go
// v4
Params: opensearchapi.SearchParams{Timeout: 5 * time.Second, Pretty: true}

// v5
Params: &opensearchapi.SearchParams{
    TimeoutParams: opensearchapi.TimeoutParams{Timeout: 5 * time.Second},
    DebugParams:   opensearchapi.DebugParams{Pretty: true},
}
```

### Optional `bool` query parameters become `*bool`

A deliberate `false` now reaches the wire. Previously, the zero value of `bool` was indistinguishable from "not set", so callers could not turn off a server-side default that the server treats as on-by-default.

```go
// v5
params := opensearchapi.SearchParams{
    AllowNoIndices: opensearch.ToPointer(false), // explicit false
}
```

`opensearch.ToPointer(v)` is a generic helper. It is deprecated; once the module's `go` directive moves to Go 1.26, `new(false)` literals work directly.

### Partial-failure type renames

The v5 and v4 `opensearchapi/` packages carry the same high-level partial-failure error types (`*PartialBulkError`, `*PartialSearchError`, `*ShardFailureError`, `*MultiSearchItemError`, `*MSearchErrors`, `*MSearchTemplateErrors`). The internal field types diverge because v5 is generated from the [OpenSearch API specification](https://github.com/opensearch-project/opensearch-api-specification):

| Field role                      | v4 (hand-written)       | v5 (generated)           |
| ------------------------------- | ----------------------- | ------------------------ |
| Per-shard failure element       | `ResponseShardsFailure` | `ShardSearchFailure`     |
| Per-sub-response error envelope | inline `*DocumentError` | embedded `ErrorRespBase` |
| Shard envelope on responses     | `ResponseShards`        | `ShardStatistics`        |

Code that only reads top-level fields (`PartialSearchError.FailedShards`, `.TotalShards`) compiles unchanged. Code that walks the per-shard failure slice needs to switch type names.

## Default Router injection

`opensearchapi.NewClient` (and `NewDefaultClient`) inject [`opensearchtransport.NewDefaultRouter`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#NewDefaultRouter) when the caller leaves `config.Client.Router` nil. v4 left Router nil.

The `OPENSEARCH_GO_ROUTER` env var acts as an opt-out (`false` / `0` keeps Router nil). See [`README.md` Default Router Injection](README.md#default-router-injection) for the full truth table and rationale.

## Errmask default flips

`Config.Errors` is a `*errmask.ErrorMask`. The default differs:

| Surface | `Config.Errors == nil` means | Effect                                            |
| ------- | ---------------------------- | ------------------------------------------------- |
| v4      | `errmask.All`                | mask everything (preserves pre-bitfield behavior) |
| v5      | `errmask.Empty`              | report every partial-failure category             |

Concretely: a v4 caller who never set `Config.Errors` does not see partial failures as `error`. The same code on v5 does.

If you need v4-shaped silence on v5, set `Errors: errmask.New(errmask.All)` explicitly. If you want to opt v4 in to v5-style surfacing, set `Errors: errmask.New()`.

The `OPENSEARCH_GO_ERROR_MASK` env var overrides whatever `Config.Errors` resolves to. See [`README.md` Partial Failure Errors](README.md#partial-failure-errors) for the full guide.

## Client method grouping

Document and point-in-time operations are reached through sub-clients on the `Client`, grouping them by API family rather than flattening every operation onto the top-level `Client`.

Document operations move onto `client.Doc`:

| v4 / earlier               | v5                             |
| -------------------------- | ------------------------------ |
| `client.Index(...)`        | `client.Doc.Index(...)`        |
| `client.Create(...)`       | `client.Doc.Create(...)`       |
| `client.Get(...)`          | `client.Doc.Get(...)`          |
| `client.GetSource(...)`    | `client.Doc.GetSource(...)`    |
| `client.Exists(...)`       | `client.Doc.Exists(...)`       |
| `client.ExistsSource(...)` | `client.Doc.ExistsSource(...)` |
| `client.Delete(...)`       | `client.Doc.Delete(...)`       |
| `client.Update(...)`       | `client.Doc.Update(...)`       |
| `client.MGet(...)`         | `client.Doc.MGet(...)`         |
| `client.Bulk(...)`         | `client.Doc.Bulk(...)`         |
| `client.BulkStream(...)`   | `client.Doc.BulkStream(...)`   |
| `client.TermVectors(...)`  | `client.Doc.TermVectors(...)`  |
| `client.MTermVectors(...)` | `client.Doc.MTermVectors(...)` |

Point-in-time operations move onto `client.PIT`, dropping the redundant `PIT`/`Pits` suffix from the method name:

| v4 / earlier                | v5                          |
| --------------------------- | --------------------------- |
| `client.CreatePIT(...)`     | `client.PIT.Create(...)`    |
| `client.DeletePIT(...)`     | `client.PIT.Delete(...)`    |
| `client.GetAllPits(...)`    | `client.PIT.GetAll(...)`    |
| `client.DeleteAllPits(...)` | `client.PIT.DeleteAll(...)` |

The request and response types are unchanged (`opensearchapi.IndexReq`, `opensearchapi.CreatePITReq`, and so on); only the canonical call path changes. The `Doc` sub-client is also reachable as `client.Document`, and `PIT` as `client.PointInTime`, for convenience.

### Backward-compatibility forwarders

To ease migration, the client ships forwarder methods so existing call sites keep compiling. These forward to the canonical sub-client method:

- Top-level `client.Bulk`, `client.MGet`, and `client.Update` forward to their `client.Doc.*` equivalents (these were top-level methods historically). `client.Index` is not retained as a top-level forwarder: `Index` is the indices sub-client field on `Client`, and a field and method of that name cannot coexist; use `client.Doc.Index`.
- `client.Document.Source` forwards to `client.Doc.GetSource`.
- `client.PointInTime.Get` forwards to `client.PIT.GetAll`.

The forwarders are generated by default. They can be suppressed at generation time with `osgen api --emit-v4-compat=false`, and marked deprecated with `--emit-v4-deprecation=true`. New code should prefer the canonical sub-client methods.

Index-template and data-stream operations are reached through `client.Indices` (e.g. `client.Indices.PutIndexTemplate`, `client.Indices.CreateDataStream`, `client.Indices.GetDataStream`); the standalone `client.IndexTemplate`, `client.ComponentTemplate`, `client.Template`, and `client.DataStream` fields are gone. Stored-script operations remain on the top-level `Client` (`client.PutScript`, `client.GetScript`, ...).

## Plugins

Plugin APIs (k-NN, ML, Security, ISM, ...) live under [`plugins/`](../plugins/README.md) as independent packages that share the same `opensearch.Client` transport.

## Quick checklist

- Update import paths from `/v4` to `/v5`.
- For operations with a typed `Body`, move raw `io.Reader` bodies from `Body` to `BodyReader`.
- Wrap `Params` literals in `&` (or use the `Params: nil` shorthand).
- Move `Timeout`/`Pretty`/`Human`/`ErrorTrace` into the embedded `TimeoutParams`/`DebugParams`.
- Wrap optional `bool` query-param values in `opensearch.ToPointer(...)`.
- Route document calls through `client.Doc.*` and point-in-time calls through `client.PIT.*`.
- Decide whether to set `Config.Errors` explicitly. v5 reports every partial-failure category by default.
- Decide whether to override `Config.Client.Router` or `OPENSEARCH_GO_ROUTER`. v5 injects the default router.
- Re-run your test suite. Spec-driven type renames in partial-failure field elements may surface as compile errors at call sites that walk shard-failure slices.

## See also

- [`README.md`](README.md) - full v5 usage guide.
- [`../guides/usage-error_handling.md`](../guides/usage-error_handling.md) - error-handling best practices.
- [`../UPGRADING.md`](../UPGRADING.md) - version-history index.
- [`plugins/README.md`](../plugins/README.md) - plugin client usage.

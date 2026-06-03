# Migrating from `opensearchapi/` to `v5preview/opensearchapi/`

This guide enumerates every code change a v4 caller needs to make to move to the v5 preview surface. The package name is identical (`opensearchapi`); only the import path changes, so most call sites only need the new import plus a handful of surface tweaks documented below.

For runtime semantics (partial-failure errors, default Router) see [`README.md`](README.md). For the version-history rationale see [`../../UPGRADING.md`](../../UPGRADING.md). For best-practices guidance that applies to both surfaces see [`../../guides/error_handling.md`](../../guides/error_handling.md).

## Status

`v5preview/opensearchapi/` is a preview of the v5 API surface, shipped inside the v4 module. Types, field names, and method shapes may change before v5 ships. Track [issue #835](https://github.com/opensearch-project/opensearch-go/issues/835) for breakage notices.

## A one-time conversion -- and why

The renames in this guide (`Indices` -> `Index`, `DocumentID` -> `ID`, optional `Params` becoming `*Params`, partial-failure type renames) are unfortunate but unavoidable: this is the one-time cost of switching `opensearchapi` from hand-written types to a code-generated client sourced from the [OpenSearch API specification](https://github.com/opensearch-project/opensearch-api-specification).

We're sorry for the churn. The trade is that future spec evolutions arrive as additive types and methods rather than coordinated rename pulls, the surface stays in lockstep with the server, and bug fixes flow through the spec instead of being re-translated by hand. After this conversion, you should not see another wave of renames at this scale.

## Import path

```go
// v4
import "github.com/opensearch-project/opensearch-go/v4/opensearchapi"

// v5preview
import "github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi"
```

The package qualifier (`opensearchapi.X`) does not change. When v5 ships, the only edit per file is dropping `/v4/v5preview` from the import path and replacing `v4` with `v5`.

### Forward-compatible `replace` directive (optional)

To write code today against the eventual v5 import path, add a `replace` to `go.mod`:

```
replace github.com/opensearch-project/opensearch-go/v5/opensearchapi => github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi v4.7.0
```

Then import as if v5 already shipped:

```go
import "github.com/opensearch-project/opensearch-go/v5/opensearchapi"
```

When v5 ships, drop the `replace` line; nothing else changes.

## Field renames you'll hit

### `Indices` -> `Index` on multi-index Req types

The OpenSearch API uses `index` (not `indices`) for multi-index path parameters; v4's hand-written types pluralized to `Indices`. v5preview matches the API.

```go
// v4
client.Search(ctx, &opensearchapi.SearchReq{
    Indices: []string{"products"},
    Body:    body,
})

// v5preview
client.Search(ctx, &opensearchapi.SearchReq{
    Index: []string{"products"},
    Body:  body,
})
```

Affected Req types include `SearchReq`, `MSearchReq`, `IndicesGetReq`, `IndicesDeleteReq`, `IndicesRefreshReq`, and any other operation whose spec definition uses an `index` path parameter accepting a list. When in doubt, the gen file is the source of truth: grep `v5preview/opensearchapi/` for the Req type and read its field list.

### Optional `Params` becomes `*Params`

```go
// v4
client.Search(ctx, &opensearchapi.SearchReq{
    Indices: []string{"products"},
    Params:  opensearchapi.SearchParams{Size: 20},
})

// v5preview
client.Search(ctx, &opensearchapi.SearchReq{
    Index:  []string{"products"},
    Params: &opensearchapi.SearchParams{Size: 20},
})
```

Pointer-typed `Params` lets callers pass `nil` when no parameters are needed and keeps the struct cheap to copy.

### Optional `bool` query parameters become `*bool`

A deliberate `false` now reaches the wire. Previously, the zero value of `bool` was indistinguishable from "not set", so callers could not turn off a server-side default that the server treats as on-by-default.

```go
// v5preview
params := opensearchapi.SearchParams{
    AllowNoIndices: opensearch.ToPointer(false), // explicit false
}
```

`opensearch.ToPointer(v)` is a generic helper. It is deprecated and will be removed in v5; once the module's `go` directive moves to Go 1.26, `new(false)` literals work directly.

### Partial-failure type renames

`v5preview/opensearchapi/` and v4's `opensearchapi/` carry the same high-level partial-failure error types (`*PartialBulkError`, `*PartialSearchError`, `*ShardFailureError`, `*MultiSearchItemError`, `*MSearchErrors`, `*MSearchTemplateErrors`). The internal field types diverge because v5preview is generated from the [OpenSearch API specification](https://github.com/opensearch-project/opensearch-api-specification):

| Field role                      | v4 (`opensearchapi`)    | v5preview (`v5preview/opensearchapi`) |
| ------------------------------- | ----------------------- | ------------------------------------- |
| Per-shard failure element       | `ResponseShardsFailure` | `ShardSearchFailure`                  |
| Per-sub-response error envelope | inline `*DocumentError` | embedded `ErrorRespBase`              |
| Shard envelope on responses     | `ResponseShards`        | `ShardStatistics`                     |

Code that only reads top-level fields (`PartialSearchError.FailedShards`, `.TotalShards`) compiles unchanged. Code that walks the per-shard failure slice needs to switch type names.

## Default Router injection

`v5preview/opensearchapi.NewClient` (and `NewDefaultClient`) inject [`opensearchtransport.NewDefaultRouter`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v4/opensearchtransport#NewDefaultRouter) when the caller leaves `config.Client.Router` nil. v4 leaves Router nil.

The `OPENSEARCH_GO_ROUTER` env var acts as an opt-out (`false` / `0` keeps Router nil). See [`README.md` Default Router Injection](README.md#default-router-injection) for the full truth table and rationale.

## Errmask default flips

`Config.Errors` is a `*errmask.ErrorMask`. The default differs:

| Surface   | `Config.Errors == nil` means | Effect                                            |
| --------- | ---------------------------- | ------------------------------------------------- |
| v4        | `errmask.All`                | mask everything (preserves pre-bitfield behavior) |
| v5preview | `errmask.Empty`              | report every partial-failure category             |

Concretely: a v4 caller who never set `Config.Errors` does not see partial failures as `error`. The same code on v5preview does.

If you need v4-shaped silence on v5preview, set `Errors: errmask.New(errmask.All)` explicitly. If you want to opt v4 in to v5-style surfacing, set `Errors: errmask.New()`.

The `OPENSEARCH_GO_ERROR_MASK` env var overrides whatever `Config.Errors` resolves to. See [`README.md` Partial Failure Errors](README.md#partial-failure-errors) for the full guide.

## Plugins

Plugin APIs (k-NN, ML, Security, ISM, ...) live under [`plugins/`](plugins/README.md) as independent packages that share the same `opensearch.Client` transport. The v4 `opensearchapi/plugins/` tree mirrors the same layout.

## Quick checklist

- Update import paths to add `/v5preview`.
- Rename `Indices` -> `Index` on Req structs that accept multi-index path parameters.
- Wrap `Params` literals in `&` (or use the `Params: nil` shorthand).
- Wrap optional `bool` query-param values in `opensearch.ToPointer(...)`.
- Decide whether to set `Config.Errors` explicitly. v5preview reports every partial-failure category by default.
- Decide whether to override `Config.Client.Router` or `OPENSEARCH_GO_ROUTER`. v5preview injects the default router.
- Re-run your test suite. Spec-driven type renames in partial-failure field elements may surface as compile errors at call sites that walk shard-failure slices.

## See also

- [`README.md`](README.md) - full v5preview usage guide.
- [`../../guides/error_handling.md`](../../guides/error_handling.md) - error-handling best practices, applicable to both v4 and v5preview.
- [`../../UPGRADING.md`](../../UPGRADING.md) - version-history index.
- [`plugins/README.md`](plugins/README.md) - plugin client usage.

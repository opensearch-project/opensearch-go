# Migrating from v3 `opensearchapi/` to v4 `opensearchapi/`

This guide enumerates the code changes a v3 caller needs to move to the v4 API surface. The package name is identical (`opensearchapi`) and the generated client and its sub-clients are unchanged, so most call sites only need the new import path (`/v3` -> `/v4`). The one change that needs a human hand is the error model, which moved out of `opensearchapi` into the root `opensearch` package.

For runtime semantics see [`README.md`](README.md). For the version-history rationale see [`../UPGRADING.md`](../UPGRADING.md). To continue on to v5 see [`UPGRADING_V4_TO_V5.md`](UPGRADING_V4_TO_V5.md).

## Automated migration

The import-path bump is applied for you by [`osapifix`](../cmd/osapifix/README.md), the API-shape migration tool in this repository. It is a separate Go module. Build the binary from an `opensearch-go` checkout, then run it against your module:

```sh
# Build the tool from an opensearch-go checkout.
git clone https://github.com/opensearch-project/opensearch-go
(cd opensearch-go/cmd/osapifix && go build -o "$(go env GOPATH)/bin/osapifix" .)

# From your module root. Source (v3) is auto-detected from imports; target defaults to the newest known (v5).
# Pass -dst=v4 to stop at v4.
osapifix rewrite -dst=v4 ./...      # dry run; review, then re-run with -w

# Bump the dependency and build.
go get github.com/opensearch-project/opensearch-go/v4 && go build ./...
```

Run without `-w` first for a dry-run preview. `rewrite` performs the import-path bump and prints the behavioral follow-ups it cannot make mechanically (below), failing loudly rather than guess if it hits an unclassified field change. It does not rewrite the error-model move: a cross-package change (`opensearchapi.Error` -> `opensearch.Error`) cannot be expressed as a mechanical rename, so it is reported, not applied. `osapifix vet` targets v5 precise types and is a no-op for a v3->v4 stop; running `rewrite` all the way to v5 (the default target) rebuilds through v4 and applies the v4->v5 changes documented in [`UPGRADING_V4_TO_V5.md`](UPGRADING_V4_TO_V5.md).

Files behind custom build tags (`//go:build <tag>`) are loaded under the default build constraints, so `rewrite` skips them without warning. Migrate any such files by hand.

## Error types moved to the root package

In v3 the API error types lived in `opensearchapi` (`opensearchapi/error.go`). In v4 they moved to the root `opensearch` package and were redesigned. Update the imports and package qualifiers by hand:

| v3                                              | v4                                                    |
| ----------------------------------------------- | ----------------------------------------------------- |
| `opensearchapi.Error` (`{Err Err; Status int}`) | `opensearch.StructError` (same shape)                 |
| —                                               | `opensearch.Error` (new, simpler `{Err string}`)      |
| `opensearchapi.Err`                             | `opensearch.Err` (adds optional `CausedBy *CausedBy`) |
| `opensearchapi.RootCause`                       | `opensearch.RootCause`                                |
| `opensearchapi.StringError`                     | `opensearch.StringError`                              |

The v3 detailed-error type `opensearchapi.Error{Err Err; Status int}` is now `opensearch.StructError`; the v4 `opensearch.Error` is a different, simpler type. Re-point type switches and assertions that decoded the detailed error to `opensearch.StructError`. `opensearch.Err` gains an optional `CausedBy *CausedBy` field for nested causes; existing field access is unaffected.

## Response types no longer expose raw maps

The remaining sections track the upcoming v4 (the `opensearch-go/v4` development head, which is what `osapifix`'s committed surfaces are generated from); a released tag such as `v4.6.0` may still expose some of these fields. The error-model move above shipped in `v4.0.0`.

Several `opensearchapi` response types dropped an exported `Indices map[string]struct{...}` field in favor of a read-only accessor (`GetIndices()`) or an embedded data struct: `AliasGetResp`, `IndicesGetResp`, `IndicesRecoveryResp`, `MappingFieldResp`, `MappingGetResp`, `SettingsGetResp`. If you read `resp.Indices`, switch to the accessor. `osapifix` reports any such access as unclassified rather than guessing a rewrite.

## Transport connection fields

`opensearchtransport.Connection` dropped its liveness fields (`DeadSince`, `Failures`, `IsDead`); it keeps `URL`/`ID`/`Name`/`Roles`/`Attributes` and adds `URLString`. Remove any code inspecting the removed fields.

## Pointerization

Many optional response fields became pointers in v4 (for example `CatNodesItemResp.CPU` `int` -> `*int`), so the zero value is distinguishable from an absent value. `osapifix` adjusts value-to-pointer assignments it can see; review numeric/string field reads that assumed a non-pointer.

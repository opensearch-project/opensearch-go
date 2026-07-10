# osapifix

`osapifix` migrates a Go module across opensearch-go major versions. The source major is detected from the module's imports and the target defaults to the newest supported version, so the common invocation is:

```sh
osapifix rewrite -w ./...
```

Today it supports the v2 -> v3, v3 -> v4, and v4 -> v5 hops, chainable to migrate across several majors at once (e.g. v3 -> v5). Most hops are added purely as data, but v2 -> v3 also required two engine additions (see [The v2 -> v3 hop](#the-v2---v3-hop)).

## Install

`osapifix` is a separate Go module. Build the binary from an `opensearch-go` checkout:

```sh
git clone https://github.com/opensearch-project/opensearch-go
(cd opensearch-go/cmd/osapifix && go build -o "$(go env GOPATH)/bin/osapifix" .)
```

The examples below assume the resulting `osapifix` binary is on your `PATH`.

## Subcommands

### `rewrite` - API-shape migration (pre-compile)

Rewrites source that uses the old major's API shapes (type names, method paths, field spellings) into the target's. It is purely syntactic (go/parser + astutil + go/printer) so it runs before the code compiles against the target.

```sh
osapifix rewrite [-src=auto] [-dst=vN] [-w] [dir]
```

- `-src` - source major (`v4`), or `auto` (default) to detect from imports.
- `-dst` - target major (`v5`), defaults to the newest supported version.
- `-w` - apply changes. Omitted, `rewrite` is a dry run that prints the edits.
- `dir` - module directory (default `.`). A `./...` pattern is accepted and resolved to its base directory.

Writes are sandboxed to the target module directory via `os.Root`.

### `vet` - runtime-hazard cleanup (post-compile)

The target's precise types (`*int64`, `*string`, ...) flow into `any` sinks such as testify's `Equal`/`Greater`, compiling cleanly but failing at run time with "Elements should be the same type". `vet` runs go/analysis analyzers (`typedassert.go`) that catch these; `-fix` applies the safe rewrites. Run it after `rewrite` and a successful build.

```sh
osapifix vet [-fix] ./...
```

## Typical flow (v4 -> v5)

```sh
osapifix rewrite -w ./...
go get github.com/opensearch-project/opensearch-go/v5 && go build ./...
osapifix vet -fix ./...
```

## How it works

Each adjacent transition (vN -> vN+1) is a `Hop`: hand-authored tables of type renames, field dispositions, method regroups, removed helpers, and semantic followups, keyed against two committed API surfaces (`surface_vN.json`). A migration request resolves to the ordered list of hops between source and target, applied one at a time - rewrite, rebuild against the intermediate version so the type-aware pass can load, then the next hop. Intermediate versions are not surfaced to the operator.

| File               | Responsibility                                                                |
| ------------------ | ----------------------------------------------------------------------------- |
| `transitions.go`   | Version-neutral types (`Major`, `Hop`) and the `surfaces` / `hops` registries |
| `hop_vN_to_vN1.go` | Hand-authored migration data for one hop                                      |
| `plan.go`          | `planChain(src, dst)` -> the ordered per-hop plans                            |
| `detect.go`        | Source major from the module's imports                                        |
| `applydelta.go`    | Type-aware AST rewriter                                                       |
| `internal/surface` | Surface model and `DeriveDelta` between two surfaces                          |
| `cmd/gensurface`   | Generates a version's committed surface JSON                                  |

### Field dispositions

A field that vanishes on the target is governed by an explicit `FieldDisposition`, matched by (source pkg + type + field):

- **rename** - rewrite to the target field. The target type is stated explicitly, so a field may move across a type rename (e.g. `DocumentGetReq#DocumentID` -> `GetReq#ID`).
- **remove** - drop the composite-literal key.
- **manual** - the field's data relocated (e.g. a response collapsed to a raw `Body`); flagged for a human.

A vanished field with no disposition fails the run with an `osapifix bug` error; the tool does not infer rename-versus-remove. Dispositions are verified against the surfaces by `TestHopFieldDispositionsAgainstSurfaces` and are established from source: response-field renames by a shared JSON wire tag, request-field renames by the v4 code that assembles the field into the spec-named element.

### Source detection

The major version is read from import paths (`.../opensearch-go/v4/...`), not `go.mod`: a partially migrated module may `require` both majors, and `go.mod` may name the target while call sites are still source-shaped. A module importing multiple majors migrates from the lowest; the rest are reported.

## Adding a hop (e.g. v3 -> v4)

1. Generate both endpoint surfaces with `cmd/gensurface`:

   ```sh
   go run ./cmd/gensurface -dir <v3-module-dir> -version v3 \
       -patterns ./opensearchapi,.,./opensearchtransport -out surface_v3.json
   ```

2. Embed each surface (`//go:embed`) in `main.go` and register it in the `surfaces` map (`transitions.go`).

3. Author `hop_v3_to_v4.go`: diff the surfaces and rule on every changed type, field, and method. Follow `hop_v4_to_v5.go`.

4. Register the hop in the `hops` map (`transitions.go`).

5. Add `hop_v3_to_v4_test.go` for version-specific facts. The drift guards validate the tables against the surfaces automatically; a `rewrite` against real v3 code fails loudly on any unruled field.

## The v2 -> v3 hop

The v3 -> v4 and v4 -> v5 hops fit the "purely additive data" model above: they are quiet boundaries where only fields of surviving types change, which the existing engine already handles. **v2 -> v3 does not fit that model** and required two engine additions, because it is the project's one structural boundary: the `opensearchapi` package was redesigned from a function-based request API (`opensearchapi.BulkRequest{...}.Do(ctx, client)`) into a typed sub-client API (`client.Bulk(ctx, BulkReq{...})`). Of 182 v2 structs, only 16 survive by name; 166 are removed outright.

That redesign is a call/response **shape change**, not a set of renames, and it is deliberately **not automated**. There are two consumer idioms; for both, `osapifix` rewrites only the import path and **reports** the rest as `MANUAL` worklist items rather than emitting a rewrite it cannot prove:

- **Idiom 1 (function API):** `opensearchapi.<X>Request{...}.Do(ctx, client)`. The v3 method returns an already-decoded typed `*Resp`, so the raw response handling (`osResp.Body`, `.StatusCode`, `.IsError()`, manual `json.Unmarshal`) that follows the call must be reworked -- a per-op semantic rewrite, not a rename.
- **Idiom 2 (root client):** `client.Ping(client.Ping.WithContext(ctx))` plus `resp.IsError()`. The root `opensearch.Client` lost all its API method fields (only `Transport` survives); the functional options collapse into a `Req` struct and the raw-response error check moves to the returned `error`.

Both transforms are documented in [`opensearchapi/UPGRADING_V2_TO_V3.md`](../../opensearchapi/UPGRADING_V2_TO_V3.md).

The two engine additions this hop required (both report-only -- they never emit a rewrite):

1. **Removed-type diagnostic.** `DeriveDelta` previously skipped a source type with no target counterpart silently; those types are now recorded in `Delta.RemovedTypes`, and the engine flags any reference to one (idiom 1's `opensearchapi.*Request` family) as a `MANUAL` worklist line. Without this the consumer would get a bare `undefined: BulkRequest` compile error instead of an actionable list.
2. **Promoted-field access resolution.** `flagFieldAccess` followed embedding to the type that literally declares a field. `gensurface` flattens promoted fields onto the embedding struct, so idiom 2's root-client methods (declared on the embedded, and removed, `opensearchapi.API`) are ruled on `opensearch.Client` in the surface. The engine now also checks the receiver type, so `client.Ping` on the root client is flagged against the `Client` disposition.

The root-client method removals themselves are authored as `ActionManual` `FieldDispositions` (idiom 2); they are report-only by design, as the transform above cannot be mechanized.

## Testing

```sh
go test ./...
```

| File                                      | Covers                                                                            |
| ----------------------------------------- | --------------------------------------------------------------------------------- |
| `plan_test.go`                            | `planChain` and `DeriveDelta` field dispositions, via synthetic v7/v8/v9 surfaces |
| `delta_test.go`                           | Drift guards over every hop's type renames and field dispositions                 |
| `hop_v3_to_v4_test.go`                    | v3 -> v4 version-specific facts                                                   |
| `hop_v4_to_v5_test.go`                    | v4 -> v5 version-specific facts                                                   |
| `hop_v2_to_v3_test.go`                    | v2 -> v3 facts: no renames, removed-type recording, root-client manual rulings    |
| `detect_test.go`                          | Source detection, version parsing, directory resolution                           |
| `internal/surface/delta_internal_test.go` | Surface diffing internals                                                         |

## Limitations

- The v2 -> v3 hop is report-only for the API redesign: it rewrites the import path and flags every removed request type (idiom 1) and removed root-client method (idiom 2) as a `MANUAL` worklist item, but does not rewrite the call/response shape change (see [The v2 -> v3 hop](#the-v2---v3-hop)). Because the intermediate module will not compile against v3 until those manual edits are made, a chained `-w` run through v2 (e.g. v2 -> v5) cannot rebuild past the v2 -> v3 step; migrate v2 -> v3 by hand first, then run the remaining hops.
- `vet` analyzers are v5-specific (`TypedAssertAnalyzer`) and target a single version; they do not chain across hops.
- A module importing multiple majors migrates from the lowest; per-import-site source selection is not implemented.
- Files behind custom build tags (`//go:build <tag>`) are loaded under the default build constraints, so they are not rewritten and are skipped without warning. Migrate those files by hand.

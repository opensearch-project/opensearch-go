# osapilint

`osapilint` migrates a Go module across opensearch-go major versions. The source major is detected from the module's imports and the target defaults to the newest supported version, so the common invocation is:

```sh
osapilint rewrite -w ./...
```

Today it supports the v2 -> v3, v3 -> v4, and v4 -> v5 hops, chainable to migrate across several majors at once (e.g. v3 -> v5). Most hops are added purely as data, but v2 -> v3 also required two linter additions (see [The v2 -> v3 hop](#the-v2---v3-hop)).

## Install

`osapilint` is a separate Go module. Build the binary from an `opensearch-go` checkout:

```sh
git clone https://github.com/opensearch-project/opensearch-go
(cd opensearch-go/cmd/osapilint && go build -o "$(go env GOPATH)/bin/osapilint" .)
```

The examples below assume the resulting `osapilint` binary is on your `PATH`.

## Subcommands

### `rewrite` - API-shape migration (pre-compile)

Rewrites source that uses the old major's API shapes (type names, method paths, field spellings) into the target's. It is purely syntactic (go/parser + astutil + go/printer) so it runs before the code compiles against the target.

```sh
osapilint rewrite [-src=auto] [-dst=vN] [-w] [dir]
```

- `-src` - source major (`v4`), or `auto` (default) to detect from imports.
- `-dst` - target major (`v5`), defaults to the newest supported version.
- `-w` - apply changes. Omitted, `rewrite` is a dry run that prints the edits.
- `dir` - module directory (default `.`). A `./...` pattern is accepted and resolved to its base directory.

Writes are sandboxed to the target module directory via `os.Root`.

### `vet` - runtime-hazard cleanup (post-compile)

The target's precise types (`*int64`, `*string`, ...) flow into `any` sinks such as testify's `Equal`/`Greater`, compiling cleanly but failing at run time with "Elements should be the same type". `vet` runs go/analysis analyzers (`linter/typedassert.go`) that catch these; `-fix` applies the safe rewrites. Run it after `rewrite` and a successful build.

```sh
osapilint vet [-fix] ./...
```

## Typical flow (v4 -> v5)

```sh
osapilint rewrite -w ./...
go get github.com/opensearch-project/opensearch-go/v5 && go build ./...
osapilint vet -fix ./...
```

## Using it as a library

`cmd/osapilint/linter` is importable: a downstream tool can run the migration against a directory without the CLI, its flags, or its stdout. Two entrypoints:

- `MigrateSDK(ctx, SDKConfig{...})` runs the opensearch-go SDK migration - the same planning and hops as `osapilint rewrite`, returning one `SDKResult` per hop (each with the per-file `Result` edits and any manual followups) instead of printing. `Src`/`Dst` of `0` mean auto-detect / newest-known; `Write` false is a dry run; `src >= dst` returns no hops and no error. Source-detection notes ride the first hop's `Warnings`, and the `context` cancels the between-hop rebuilds.
- `Walk(WalkConfig{...}, visit)` drives a caller-supplied `Visitor` over the module's loaded files, for a migration this package does not ship (e.g. an opensearchtools overlay). It keeps the same load gate, cross-variant dedupe, and abort-before-write-on-unclassified safety net.

```go
import "github.com/opensearch-project/opensearch-go/v5/cmd/osapilint/linter"

// opensearch-go SDK migration: auto-detect source, dry run to the newest version.
hops, err := linter.MigrateSDK(ctx, linter.SDKConfig{Dir: dir})
for _, h := range hops {
    fmt.Printf("v%d -> v%d: %d file(s)\n", h.From, h.To, len(h.Files))
}
```

`Rewrite(args)` (the `rewrite` subcommand) is a thin CLI shell over `MigrateSDK`, so the command and the library apply identical edits.

## How it works

Each adjacent transition (vN -> vN+1) is a `hop`: hand-authored tables of type renames, field dispositions, method regroups, removed helpers, and semantic followups, keyed against two committed API surfaces (`surface_vN.json`). A migration request resolves to the ordered list of hops between source and target, applied one at a time - rewrite, rebuild against the intermediate version so the type-aware pass can load, then the next hop. Intermediate versions are not surfaced to the operator.

| File                      | Responsibility                                                                |
| ------------------------- | ----------------------------------------------------------------------------- |
| `linter/transitions.go`   | Version-neutral types (`major`, `hop`) and the `surfaces` / `hops` registries |
| `linter/hop_vN_to_vN1.go` | Hand-authored migration data for one hop                                      |
| `linter/plan.go`          | `planChain(src, dst)` -> the ordered per-hop plans                            |
| `linter/detect.go`        | Source major from the module's imports                                        |
| `linter/applydelta.go`    | Type-aware AST rewriter                                                       |
| `internal/apirev`         | Surface model and `DeriveDelta` between two surfaces                          |
| `cmd/gensurface`          | Generates a version's committed surface JSON                                  |

### Field dispositions

A field that vanishes on the target is governed by an explicit `FieldDisposition`, matched by (source pkg + type + field):

- **rename** - rewrite to the target field. The target type is stated explicitly, so a field may move across a type rename (e.g. `DocumentGetReq#DocumentID` -> `GetReq#ID`).
- **remove** - drop the composite-literal key.
- **manual** - the field's data relocated (e.g. a response collapsed to a raw `Body`); flagged for a human.

A vanished field with no disposition fails the run with an `osapilint bug` error; the tool does not infer rename-versus-remove. Dispositions are verified against the surfaces by `TestHopFieldDispositionsAgainstSurfaces` and are established from source: response-field renames by a shared JSON wire tag, request-field renames by the v4 code that assembles the field into the spec-named element.

### Source detection

The major version is read from import paths (`.../opensearch-go/v4/...`), not `go.mod`: a partially migrated module may `require` both majors, and `go.mod` may name the target while call sites are still source-shaped. A module importing multiple majors migrates from the lowest; the rest are reported.

## Adding a hop (e.g. v3 -> v4)

1. Generate both endpoint surfaces with `cmd/gensurface`:

   ```sh
   go run ./cmd/gensurface -dir <v3-module-dir> -version v3 \
       -patterns ./opensearchapi,.,./opensearchtransport -out surface_v3.json
   ```

2. Embed each surface (`//go:embed`) in `linter/embed.go` and register it in the `surfaces` map (`linter/transitions.go`).

3. Author `linter/hop_v3_to_v4.go`: diff the surfaces and rule on every changed type, field, and method. Follow `linter/hop_v4_to_v5.go`.

4. Register the hop in the `hops` map (`linter/transitions.go`).

5. Add `linter/hop_v3_to_v4_test.go` for version-specific facts. The drift guards validate the tables against the surfaces automatically; a `rewrite` against real v3 code fails loudly on any unruled field.

## The v2 -> v3 hop

The v3 -> v4 and v4 -> v5 hops fit the "purely additive data" model above: quiet boundaries where only fields of surviving types change, which the existing linter handles. **v2 -> v3 does not.** It is the project's one structural boundary: the `opensearchapi` package was redesigned from a function-based request API (`opensearchapi.BulkRequest{...}.Do(ctx, client)`) into a typed sub-client API (`client.Bulk(ctx, BulkReq{...})`). Of 182 v2 structs, only 16 survive by name; the other 166 are removed. That is a call/response **shape change**, not a set of renames, so the hop needed two linter additions on top of the data model.

Consumers use one of two idioms, handled differently:

- **Idiom 1 (function API):** `opensearchapi.<X>Request{...}.Do(ctx, client)`. The v3 method returns an already-decoded typed `*Resp`, so the raw response handling (`osResp.Body`, `.StatusCode`, `.IsError()`, manual `json.Unmarshal`) that follows the call must be reworked -- a per-op semantic rewrite, not a rename. This is **not automated**: `osapilint` rewrites the import path and **reports** the rest as `MANUAL` worklist items rather than emitting a rewrite it cannot prove.
- **Idiom 2 (root client):** `client.Ping(client.Ping.WithContext(ctx))` plus `resp.IsError()`. The root `opensearch.Client` lost all its API method fields (only `Transport` survives); the functional options collapse into a `Req` struct and the raw-response error check moves to the returned `error`. For the two seed ops (`Ping`, `Indices.Exists`) this is now rewritten best-effort (see [Idiom-2 best-effort rewrite](#idiom-2-best-effort-rewrite) below); every other root-client op stays `MANUAL`.

The two linter additions this hop required (both report-only -- they never emit a rewrite):

1. **Removed-type diagnostic.** `DeriveDelta` previously skipped a source type with no target counterpart silently; those types are now recorded in `Delta.RemovedTypes`, and the linter flags any reference to one (idiom 1's `opensearchapi.*Request` family) as a `MANUAL` worklist line. Without this the consumer would get a bare `undefined: BulkRequest` compile error instead of an actionable list.
2. **Promoted-field access resolution.** `flagFieldAccess` followed embedding to the type that literally declares a field. `gensurface` flattens promoted fields onto the embedding struct, so idiom 2's root-client methods (declared on the embedded, and removed, `opensearchapi.API`) are ruled on `opensearch.Client` in the surface. The linter now also checks the receiver type, so `client.Ping` on the root client is flagged against the `Client` disposition.

The root-client method removals themselves are authored as `ActionManual` `FieldDispositions` (idiom 2). These two additions are report-only; the seed-op rewrite that builds on them is described next.

### Idiom-2 best-effort rewrite

For the two seed operations, `Ping` and `Indices.Exists`, `osapilint rewrite` does a best-effort rewrite of the whole idiom-2 call site (the `client.Ping(...)` root-client family), not just the import path:

- **Call shape.** `client.Ping(client.Ping.WithContext(ctx))` becomes `client.Ping(ctx, &opensearchapi.PingReq{})`. The functional options (`WithContext`, positionals, and known `Params`-bound options such as `WithLocal`) are lifted into the `Req` struct, and `WithContext` becomes the leading `ctx` argument.
- **Raw-response `Status()`.** `resp.Status()` becomes `fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))`, with `fmt` and `net/http` injected into imports. This reproduces the v2 `"200 OK"` text; v3's own `Status()` formats it differently. The rewrite is node-local, so where the call sat inside another call (e.g. `fmt.Errorf("...: %s", resp.Status())`) the result nests as `fmt.Errorf("...: %s", fmt.Sprintf(...))` -- correct, if a touch verbose; collapse it by hand if you prefer. A comment adjacent to a rewritten `resp.Status()` may be repositioned mid-expression by the printer -> review the diff around each rewritten `Status()`.
- **Client lifecycle.** The `opensearch.Config{...}` composite literal is wrapped as `opensearchapi.Config{Client: opensearch.Config{...}}`. Post-construction field assignments (`cfg.Username`, `cfg.Password`, and so on) are rewritten to `cfg.Client.Username`, `cfg.Client.Password`. The client field type `*opensearch.Client` and its constructor `opensearch.NewClient(cfg)` are repointed to `*opensearchapi.Client` and `opensearchapi.NewClient(cfg)`, since the v3 root client no longer carries the API methods.
- **Pointer params.** A `*bool` v3 `Params` field (`Local`, `FlatSettings`, `IgnoreUnavailable`, `IncludeDefaults`, `AllowNoIndices`) is wrapped: `WithLocal(true)` becomes `Local: opensearchapi.ToPointer(true)`.
- **Import path.** The v2 import path is bumped to v3. If repointing `*opensearch.Client` to `*opensearchapi.Client` leaves the root import with no remaining references, the now-dead root import is dropped so the output does not fail with "imported and not used".

**Scope: seed ops only.** Only `Ping` and `Indices.Exists` are rewritten this increment. Every other v2 root-client method (`client.Bulk`, `client.Index`, `client.Search`, ...) stays a `MANUAL` worklist item: the rewriter logs it but leaves the call alone.

**The non-guessing invariant.** The pass applies a transform only when it is mechanically certain. Where it cannot prove the result correct, it plants the undefined sentinel `_OSAPILINT_RESOLVE` plus a salvage comment (naming what could not be placed, followed by a hand-migration hint pointing back to this section) instead of emitting a plausible but possibly wrong value, so `go build` breaks at that spot rather than compiling something quietly wrong. Cases that produce a marker:

- `WithContext` is absent (the tool will not invent a `context.Background()` for you).
- An option is dropped in v3 (`WithFilterPath`); the salvage comment names it.
- An option changes shape rather than renaming (`WithHeader`, `WithOpaqueID`).
- A response method is removed (`Warnings()`, `HasWarnings()`) or has no faithful one-liner equivalent (`String()`).
- A value carried verbatim into the v3 `Req` (a positional, an option value, or the ctx arg) still contains a v2 root-client reference the pass would otherwise leave un-migrated; the salvage comment names the field.

After `osapilint rewrite -w`, `go build` fails at each marker, and `grep -r _OSAPILINT_RESOLVE .` gives the worklist.

**Compiles and behavior-preserving, not textually identical to a hand migration.** The rewriter keeps the response variable and its surviving checks (`resp.IsError()`, `resp.Body.Close()`, `resp.StatusCode`). A person migrating by hand might drop the response altogether, since v3's typed methods return an `error` directly. Both forms compile and behave the same, so the output is not meant to match a hand-written migration line for line.

## Testing

```sh
go test ./...
```

| File                                     | Covers                                                                              |
| ---------------------------------------- | ----------------------------------------------------------------------------------- |
| `linter/plan_test.go`                    | `planChain` and `DeriveDelta` field dispositions, via synthetic v7/v8/v9 surfaces   |
| `linter/delta_test.go`                   | Drift guards over every hop's type renames and field dispositions                   |
| `linter/rewrite_corpus_test.go`          | End-to-end rewrite over fixture modules (`testdata/corpus`), diffed against goldens |
| `linter/hop_v3_to_v4_test.go`            | v3 -> v4 version-specific facts                                                     |
| `linter/hop_v4_to_v5_test.go`            | v4 -> v5 version-specific facts                                                     |
| `linter/hop_v2_to_v3_test.go`            | v2 -> v3 facts: no renames, removed-type recording, root-client manual rulings      |
| `linter/detect_test.go`                  | Source detection, version parsing, directory resolution                             |
| `internal/apirev/delta_internal_test.go` | Surface diffing internals                                                           |

### Rewrite corpus

`linter/rewrite_corpus_test.go` runs the real type-aware rewrite over small fixture modules under `linter/testdata/corpus` and diffs the output against committed `.golden` files. Each fixture compiles against a hand-written stub of the source-version API (`linter/testdata/corpus/stub-vN`), so the test needs no opensearch-go download. The v2 corpus covers both idioms: `seedops.go` (idiom 2, rewritten to compiling v3, golden-checked) and `bulk_idiom1.go` (idiom 1, report-only, checked by its removed-type MANUAL line). Fixtures meant to be pure compiling target-version output (e.g. `paramsemit.go`) are additionally asserted marker-free and import-clean, since the test does not run `go build`. The v3 corpus checks the quiet v3 -> v4 import bump. Regenerate goldens after an intentional rewrite change with `UPDATE_GOLDEN=1 go test ./linter -run TestRewriteCorpus`.

## Limitations

The v2 -> v3 hop is the only one with structural limits worth enumerating; the others are quiet data hops. Each entry below names what the tool does not rewrite and the before/after edit you finish by hand.

### v2 -> v3: idiom 1 (function API) is report-only

`osapilint` bumps the import path and reports each removed request type as a `MANUAL` worklist line, but does not rewrite the call or the raw-response block that follows it. The v3 method returns an already-decoded typed `*Resp`, so the response handling is control-flow surgery, not a rename.

```go
// before (v2)
res, err := opensearchapi.BulkRequest{Body: body}.Do(ctx, client)
if err != nil {
    return err
}
defer res.Body.Close()
if res.IsError() {
    return fmt.Errorf("bulk failed: %s", res.Status())
}
var out BulkResponse
json.NewDecoder(res.Body).Decode(&out)

// after (v3, by hand) - typed sub-client, decoded response, error returned directly
resp, err := client.Bulk(ctx, opensearchapi.BulkReq{Body: body})
if err != nil {
    return err
}
// resp is *opensearchapi.BulkResp; fields are already decoded
```

### v2 -> v3: idiom 2 (root client) rewrites seed ops only

`Ping` and `Indices.Exists` are rewritten best-effort (call shape, params, client lifecycle, imports). Every other root-client op (`client.Bulk`, `client.Search`, ...) stays a `MANUAL` line.

```go
// before (v2)
resp, err := client.Ping(client.Ping.WithContext(ctx))

// after (v3, automatic for Ping/Indices.Exists)
resp, err := client.Ping(ctx, &opensearchapi.PingReq{})
```

Where a value can't be derived mechanically, the pass plants the undefined sentinel `_OSAPILINT_RESOLVE` plus a salvage comment instead of guessing, so `go build` breaks at that spot. See [Idiom-2 best-effort rewrite](#idiom-2-best-effort-rewrite) for the full transform and the marker cases.

### Chained v2 -> v5 stops at v2 -> v3

Because an idiom-1 module won't compile against v3 until its response blocks are ported by hand, a chained `-w` run through v2 cannot rebuild past the v2 -> v3 step. Migrate v2 -> v3 by hand first (idiom 1) or lean on the seed-op rewrite (idiom 2), get a compiling v3 tree, then run the remaining hops.

### Other limits

- The removed-type diagnostic runs on every hop, not only v2 -> v3. Any reference to a type deleted across a transition (for example the many `opensearchapi` types dropped in v5) is reported as a `MANUAL` worklist line rather than silently dropped, and only when a consumer actually references it. It reports; it does not rewrite, since a removed type has no mechanical counterpart.
- `vet` analyzers are v5-specific (`typedAssertAnalyzer`) and target a single version; they do not chain across hops.
- A module importing multiple majors migrates from the lowest; per-import-site source selection is not implemented.
- Files behind custom build tags (`//go:build <tag>`) are loaded under the default build constraints, so they are not rewritten and are skipped without warning. Migrate those files by hand.

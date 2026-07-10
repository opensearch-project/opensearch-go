# osapifix

`osapifix` migrates a Go module across opensearch-go major versions. The source major is detected from the module's imports and the target defaults to the newest supported version, so the common invocation is:

```sh
osapifix rewrite -w ./...
```

Today it supports the v4 -> v5 hop. Additional hops (v3 -> v4, v2 -> v3) are added as data, without engine changes.

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

## Testing

```sh
go test ./...
```

| File                                      | Covers                                                                            |
| ----------------------------------------- | --------------------------------------------------------------------------------- |
| `plan_test.go`                            | `planChain` and `DeriveDelta` field dispositions, via synthetic v7/v8/v9 surfaces |
| `delta_test.go`                           | Drift guards over every hop's type renames and field dispositions                 |
| `hop_v4_to_v5_test.go`                    | v4 -> v5 version-specific facts                                                   |
| `detect_test.go`                          | Source detection, version parsing, directory resolution                           |
| `internal/surface/delta_internal_test.go` | Surface diffing internals                                                         |

## Limitations

- `vet` analyzers are v5-specific (`TypedAssertAnalyzer`) and target a single version; they do not chain across hops.
- A module importing multiple majors migrates from the lowest; per-import-site source selection is not implemented.

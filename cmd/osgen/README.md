# osgen

Generates API consumer files and typed path builder structs for the OpenSearch Go client from the OpenSearch OpenAPI specification.

## Quick Start

From the repository root:

```
make gen
```

This downloads the spec (if not cached) and regenerates all generated source files.

## Usage

From within `cmd/osgen/`:

```
go run . <command> [flags]
```

### Commands

| Command | Description                                           |
| ------- | ----------------------------------------------------- |
| `paths` | Generate typed path builder structs from the spec     |
| `api`   | Generate API consumer files (Req, Params, Resp) types |

Run `go run . <command> -help` for command-specific flags.

---

### paths

Generates path builder structs into `internal/path/`.

#### Flags

| Flag                             | Default  | Description                                                             |
| -------------------------------- | -------- | ----------------------------------------------------------------------- |
| `-spec`                          | required | Path to the combined OpenAPI spec YAML                                  |
| `-pkg`                           | `path`   | Output package name                                                     |
| `-o`                             | stdout   | Output file for builder structs                                         |
| `-test-out`                      | (none)   | Output file for golden tests                                            |
| `-groups`                        | (all)    | Comma-separated `x-operation-group` filter                              |
| `-min-version`                   | `epoch`  | Minimum OpenSearch version (default operator: `>=`)                     |
| `-max-version`                   | `latest` | Maximum OpenSearch version (default operator: `<=`)                     |
| `-remove-deprecated`             | `epoch`  | Treat operations deprecated at or before this version as removed        |
| `-min-version-preserve-optional` | `false`  | Keep version-gated fields as pointers even when min-version covers them |
| `-version-breadcrumb-operations` | `all`    | Emit comments for excluded operations: `all`, `older`, `newer`          |
| `-version-breadcrumb-types`      | `all`    | Emit comments for excluded types: `all`, `older`, `newer`               |
| `-version-breadcrumb-fields`     | `all`    | Emit comments for excluded struct fields: `all`, `older`, `newer`       |
| `-version-breadcrumb-paths`      | `all`    | Emit comments for excluded path builders: `all`, `older`, `newer`       |
| `-version-breadcrumb-params`     | `all`    | Emit comments for excluded query parameters: `all`, `older`, `newer`    |

#### Examples

Regenerate path builders:

```
cd cmd/osgen
go run . paths \
  -spec ../../opensearch-openapi.yaml \
  -pkg path \
  -o ../../internal/path/builders_gen.go \
  -test-out ../../internal/path/builders_gen_test.go
```

Preview a single operation group on stdout:

```
cd cmd/osgen
go run . paths \
  -spec ../../opensearch-openapi.yaml \
  -groups indices.get_alias
```

---

### api

Generates API consumer files into `opensearchapi/` and plugin directories.

#### Flags

| Flag                             | Default  | Description                                                                 |
| -------------------------------- | -------- | --------------------------------------------------------------------------- |
| `-spec`                          | required | Path to the combined OpenAPI spec YAML                                      |
| `-out`                           | required | Output directory for core API files (e.g. `opensearchapi/`)       |
| `-pkg`                           | required | Go package name for generated files (e.g. `opensearchapi`)                  |
| `-plugins-out`                   | (none)   | Output directory for plugin files (e.g. `opensearchapi/plugins/`) |
| `-groups`                        | (all)    | Comma-separated `x-operation-group` filter                                  |
| `-min-version`                   | `epoch`  | Minimum OpenSearch version (default operator: `>=`)                         |
| `-max-version`                   | `latest` | Maximum OpenSearch version (default operator: `<=`)                         |
| `-remove-deprecated`             | `epoch`  | Treat operations deprecated at or before this version as removed            |
| `-min-version-preserve-optional` | `false`  | Keep version-gated fields as pointers even when min-version covers them     |
| `-version-breadcrumb-operations` | `all`    | Emit comments for excluded operations: `all`, `older`, `newer`              |
| `-version-breadcrumb-types`      | `all`    | Emit comments for excluded types: `all`, `older`, `newer`                   |
| `-version-breadcrumb-fields`     | `all`    | Emit comments for excluded struct fields: `all`, `older`, `newer`           |
| `-version-breadcrumb-paths`      | `all`    | Emit comments for excluded path builders: `all`, `older`, `newer`           |
| `-version-breadcrumb-params`     | `all`    | Emit comments for excluded query parameters: `all`, `older`, `newer`        |

#### Examples

Regenerate all API consumer files:

```
cd cmd/osgen
go run . api \
  -spec ../../opensearch-openapi.yaml \
  -out ../../opensearchapi \
  -pkg opensearchapi \
  -plugins-out ../../opensearchapi/plugins
```

Generate a single operation:

```
cd cmd/osgen
go run . api \
  -spec ../../opensearch-openapi.yaml \
  -out ../../opensearchapi \
  -pkg opensearchapi \
  -groups cluster.health
```

## Environment Variables

| Variable               | Default | Description                                                                                                                                                                                             |
| ---------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `OSGEN_SKIP_GIT_CHECK` | `false` | Set to `1` or `true` to disable the git-toplevel safety check. Useful in CI environments where the generator runs outside a git working tree. Accepts any value recognized by Go's `strconv.ParseBool`. |

## How It Works

### Output Behavior

- **Idempotent**: Only files whose content actually changed are written. Running `make gen` twice produces no output on the second run.
- **Atomic writes**: Files are written atomically via [renameio](https://github.com/google/renameio) to avoid partial output on failure.
- **Stale removal**: Any `*_gen.go` file in the output directory that was not produced by the current run is removed automatically.
- **Git safety**: Output directories are validated to be inside the git working tree (via `git rev-parse --show-toplevel` and `os.OpenRoot`). Set `OSGEN_SKIP_GIT_CHECK=1` to bypass when running outside a git repository.
- **Multi-method operations**: When an operation accepts multiple HTTP methods (e.g., GET and POST), the generated `GetRequest` uses POST when a body is present.

### Path Generation

1. Parses the OpenAPI spec with `kin-openapi` and groups paths by their `x-operation-group` extension.
2. For each group, builds a trie of path variants and walks it via DFS.
3. Determines required vs optional parameters (required = present in ALL variants for that group).
4. Applies subsumption optimization (list-type params subsume their literal alternatives; scalar params use guard + shared suffix hoisting).
5. Emits a Go struct with typed fields and a `Build()` method that uses a pooled byte buffer for zero-allocation path construction.
6. Generates golden tests by simulating `Build()` with synthetic inputs.

### API Generation

1. Extracts operations from the spec, resolving HTTP methods, path patterns, query parameters, and request bodies.
2. Classifies query parameters into Go types (string, int, bool, duration, list) based on schema analysis.
3. Routes each operation to either the core `opensearchapi` package or a plugin package based on the operation group prefix.
4. Renders Req structs (with path builder embedding, optional body, and header support), Params structs (with typed encode methods), and Resp stubs.
5. Annotates generated code with availability (`x-version-added`), deprecation (`x-version-deprecated`, `x-deprecation-message`), and distribution exclusion metadata.
6. Reads each operation's `x-error-responses` extension to emit typed partial-failure errors (`*PartialBulkError`, `*PartialSearchError`, `*ShardFailureError`, `*MultiSearchItemError`, ...), the corresponding `errmask` bits and env-var tokens, per-Resp helper methods (`BulkItemFailures()`, `SearchShardFailures()`, `WriteShardFailures()`, `MultiSearchItemFailures()`, `PartialFailures(mask)`) used internally by the dispatch, and -- for operations declaring two or more categories -- a per-op multi-error container implementing `Unwrap() []error`. The recommended call-site pattern in user code is a `for`/`switch` over `opensearchapi.Errors(err)`, not the per-Resp helpers; see [`DEVELOPER_GUIDE.md` Partial-failure error generation](../../DEVELOPER_GUIDE.md#partial-failure-error-generation) for the generated surface and [`opensearchapi/README.md` Partial Failure Errors](../../opensearchapi/README.md#partial-failure-errors) for the user-facing usage guide.

## Separate Module

This tool has its own `go.mod` to keep `kin-openapi` and its transitive dependencies out of the client library's dependency graph. The `//go:generate` directive in `internal/path/doc.go` invokes `osgen` from within this directory.

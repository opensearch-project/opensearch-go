# osgen

Reads the OpenSearch OpenAPI specification and generates typed path builder structs grouped by `x-operation-group`. Each struct constructs the URL path for one logical API operation.

## Quick Start

From the repository root:

```
make gen
```

This downloads the spec (if not cached) and regenerates `internal/path/builders_gen.go` and `internal/path/builders_gen_test.go`.

## Usage

```
go run -modfile cmd/osgen/go.mod ./cmd/osgen [flags]
```

### Flags

| Flag        | Default    | Description                                |
| ----------- | ---------- | ------------------------------------------ |
| `-spec`     | (required) | Path to the combined OpenAPI spec YAML     |
| `-pkg`      | `path`     | Output package name                        |
| `-o`        | stdout     | Output file for builder structs            |
| `-test-out` | (none)     | Output file for golden tests               |
| `-groups`   | (all)      | Comma-separated `x-operation-group` filter |

### Examples

Regenerate everything:

```
go run -modfile cmd/osgen/go.mod ./cmd/osgen \
  -spec opensearch-openapi.yaml \
  -pkg path \
  -o internal/path/builders_gen.go \
  -test-out internal/path/builders_gen_test.go
```

Preview a single operation group on stdout:

```
go run -modfile cmd/osgen/go.mod ./cmd/osgen \
  -spec opensearch-openapi.yaml \
  -groups indices.get_alias
```

## How It Works

1. Parses the OpenAPI spec with `kin-openapi` and groups paths by their `x-operation-group` extension
2. For each group, builds a trie of path variants and walks it via DFS
3. Determines required vs optional parameters (required = present in ALL variants for that group)
4. Applies subsumption optimization (list-type params subsume their literal alternatives; scalar params use guard + shared suffix hoisting)
5. Emits a Go struct with typed fields and a `Build()` method that uses a pooled byte buffer for zero-allocation path construction
6. Generates golden tests by simulating `Build()` with synthetic inputs

## Separate Module

This tool has its own `go.mod` to keep `kin-openapi` and its transitive dependencies out of the client library's dependency graph. The `//go:generate` directive in `internal/path/doc.go` uses `-modfile` to reference this module.

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

| Flag        | Default    | Description                                |
| ----------- | ---------- | ------------------------------------------ |
| `-spec`     | (required) | Path to the combined OpenAPI spec YAML     |
| `-pkg`      | `path`     | Output package name                        |
| `-o`        | stdout     | Output file for builder structs            |
| `-test-out` | (none)     | Output file for golden tests               |
| `-groups`   | (all)      | Comma-separated `x-operation-group` filter |

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

| Flag           | Default    | Description                                          |
| -------------- | ---------- | ---------------------------------------------------- |
| `-spec`        | (required) | Path to the combined OpenAPI spec YAML               |
| `-out`         | (required) | Output directory for core API files (opensearchapi/) |
| `-plugins-out` | (none)     | Output directory for plugin files (plugins/)         |
| `-groups`      | (all)      | Comma-separated `x-operation-group` filter           |

#### Examples

Regenerate all API consumer files:

```
cd cmd/osgen
go run . api \
  -spec ../../opensearch-openapi.yaml \
  -out ../../opensearchapi \
  -plugins-out ../../plugins
```

Generate a single operation:

```
cd cmd/osgen
go run . api \
  -spec ../../opensearch-openapi.yaml \
  -out ../../opensearchapi \
  -groups cluster.health
```

## How It Works

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

## Separate Module

This tool has its own `go.mod` to keep `kin-openapi` and its transitive dependencies out of the client library's dependency graph. The `//go:generate` directive in `internal/path/doc.go` invokes `osgen` from within this directory.

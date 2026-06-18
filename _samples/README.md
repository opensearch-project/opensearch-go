# OpenSearch Golang Samples

Most samples can be run using OpenSearch installed locally with docker.

Start container:

```
make cluster.build cluster.start
```

Stop and cleanup:

```
make cluster.stop cluster.clean
```

## Run Samples

```
go run _samples/usage-json.go
```

## Samples

Each sample is a standalone `package main`. Filenames are grouped by subsystem with the same prefixes as the [`guides/`](../guides/README.md): `transport-`, `indexing-`, `usage-`, and `config-`.

### Transport

Connection, routing, and node-discovery behavior.

- `transport-discovery_demo.go` - end-to-end node discovery flow with role-based request routing and metrics.

### Indexing

Writing and managing indices and documents.

- `indexing-bulk.go` - bulk index/update/delete in a single request.
- `indexing-document_lifecycle.go` - create, read, update, and delete documents.
- `indexing-index_lifecycle.go` - create, configure, and delete indices.
- `indexing-advanced_index_actions.go` - clear cache, flush, refresh, force merge, and other index actions.
- `indexing-data_stream.go` - create and manage a data stream.

### Usage

General client usage: querying, raw requests, and async operations.

- `usage-search.go` - search an index and read hits.
- `usage-json.go` - issue raw JSON REST requests when no typed method fits.
- `usage-tasks.go` - submit and poll long-running async tasks.

### Config

Client construction and configuration.

- `config-client_from_existing.go` - build an `opensearchapi.Client` from an existing `opensearch.Client`.
- `config-client_config_retrieval.go` - construct a client from retrieved configuration.

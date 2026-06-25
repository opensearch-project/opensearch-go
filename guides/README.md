# Guides

Task-oriented guides for the OpenSearch Go client. Each guide is the canonical reference for its topic; cross-links point to the single source of truth rather than duplicating content.

## Reading and Writing Data

- [Document Lifecycle](indexing-document_lifecycle.md) - Create, read, update, and delete individual documents.
- [Bulk](indexing-bulk.md) - Index, update, and delete many documents in a single request.
- [Search](usage-search.md) - Query an index and shape the results with search parameters.
- [Making Raw JSON REST Requests](usage-json.md) - Reach endpoints that have no typed method yet by sending a raw JSON body.

## Managing Indices

- [Index Lifecycle](indexing-index_lifecycle.md) - Create, configure, update, and delete indices.
- [Index Template](indexing-index_template.md) - Apply settings, mappings, and aliases to indices matching a name pattern.
- [Advanced Index Actions](indexing-advanced_index_actions.md) - Clear cache, flush, refresh, force merge, and other maintenance actions.
- [Data Streams](indexing-data_streams.md) - Manage append-only time-series data streams.

## Connections, Routing, and Discovery

- [Request Routing and Connection Management](transport-routing.md) - The per-request scoring model that routes operations to shard-hosting nodes by proximity, load, and data placement.
- [Node Discovery and Role Management](transport-node_discovery_and_roles.md) - Discover cluster nodes and route by node role.
- [Cluster Health Checking](transport-cluster_health_checking.md) - Two-phase health checks and capability detection, including the permissions the Security plugin requires.
- [Retry and Backoff](transport-retry_backoff.md) - Tune request retries and dead-connection resurrection backoff.

## Responses and Error Handling

- [Error Handling and Partial Failures](usage-error_handling.md) - The canonical reference for detecting and handling partial failures, including the typed error model and helpers.
- [Response Body Lifecycle: `Do[T]` vs `Stream`](transport-response_buffering.md) - Choose between the buffered and streaming entry points and understand their body-ownership contracts.

## Operations and Observability

- [Tasks](usage-tasks.md) - Submit long-running operations asynchronously, poll for completion, and inspect task status.
- [Client-Side Metrics](transport-metrics.md) - Read a point-in-time snapshot of request counters, connection-pool state, and router cache state.

## Configuration and Security

- [Environment Variables](config-envvars.md) - The canonical reference for every `OPENSEARCH_GO_*` runtime variable.
- [Security](config-security.md) - TLS, certificate verification, authentication, and related configuration.

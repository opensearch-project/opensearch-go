# Environment Variables

Canonical reference for every `OPENSEARCH_GO_*` runtime environment variable. Each variable is read once at client initialization and is immutable after. Environment variable values override their programmatic configuration equivalents.

## Parsing rules

**Boolean variables.** Parsed with Go's `strconv.ParseBool`. Truthy values: `1`, `t`, `T`, `TRUE`, `true`, `True`. Falsy values: `0`, `f`, `F`, `FALSE`, `false`, `False`. Empty, unset, or unparseable values fall back to the documented default.

**Duration variables.** Accept a Go `time.ParseDuration` string (e.g. `500ms`, `2s`, `1m30s`) or a bare integer/float number of seconds.

**The "See also" column links to the guide section that explains the _concept_ the variable tunes. This document owns the value table; the linked section owns the explanation. No concept is described in two places.**

---

## Connection

| Variable | Accepted values | Default | Meaning | See also |
| --- | --- | --- | --- | --- |
| `OPENSEARCH_URL` | Comma-separated URL list (e.g. `https://a:9200,https://b:9200`) | unset | Seed addresses used by `NewClient` when no `Addresses` are set programmatically. | [opensearchapi/README.md Client Creation](../opensearchapi/README.md#client-creation); [Security: Credential Management](security.md#credential-management) |

## Routing

| Variable | Accepted values | Default | Meaning | See also |
| --- | --- | --- | --- | --- |
| `OPENSEARCH_GO_ROUTER` | Bool | `true` (router auto-constructed) | Auto-construct `NewDefaultRouter()` when no programmatic `Config.Router` is set. Set `false` or `0` to suppress. A programmatic `Config.Router` always takes precedence. | [Routing: Quick Start](routing.md#quick-start); [opensearchapi/README.md Default Router Injection](../opensearchapi/README.md#default-router-injection) |
| `OPENSEARCH_GO_ROUTING_CONFIG` | Comma-separated flags/key=value | all enabled | Toggle shard-exact routing (`-shard_exact`) and adaptive MCSR (`-adaptive_mcsr`). | [Routing: Configuration Reference](routing.md#routing-and-discovery) |
| `OPENSEARCH_GO_SHARD_COST` | Key=value, comma-separated (or bare numeric for `r:base`) | compile-time defaults | Override shard cost multipliers used in connection scoring. Format: key=value pairs (`r:base=0.95`, `unknown=32.0`) or a bare number (sets `r:base`). | [Routing: Shard Cost Configuration](routing.md#shard-cost-configuration) |
| `OPENSEARCH_GO_SHARD_REQUESTS` | Bool or `min:max` | `true` (5:256) | Adaptive `max_concurrent_shard_requests` bounds. `true`/`false` enable/disable with defaults; `10:512`, `10:`, `:512`, or bare `10` set custom bounds. | [Routing: Configuration Reference](routing.md#routing-and-discovery) |

## Discovery

| Variable                         | Accepted values       | Default     | Meaning                                                                                                       | See also                                                             |
| -------------------------------- | --------------------- | ----------- | ------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------- |
| `OPENSEARCH_GO_DISCOVERY_CONFIG` | Comma-separated flags | all enabled | Skip specific discovery calls. Flags: `-cat_shards`, `-routing_num_shards`, `-cluster_health`, `-node_stats`. | [Routing: Configuration Reference](routing.md#routing-and-discovery) |
| `OPENSEARCH_GO_FALLBACK`         | Bool                  | `true`      | Seed URL fallback when all router pools are exhausted. Set `false` to disable.                                | [Routing: Configuration Reference](routing.md#routing-and-discovery) |

## Load shedding and stats polling

| Variable | Accepted values | Default | Meaning | See also |
| --- | --- | --- | --- | --- |
| `OPENSEARCH_GO_REQUEST_TIMEOUT` | Duration or seconds | `0` (none) | Per-attempt HTTP round-trip timeout. | [Security: Connection Timeouts](security.md#connection-timeouts) |
| `OPENSEARCH_GO_NODE_STATS_INTERVAL` | Duration or seconds | auto (5s-30s) | Stats polling interval. `0` or unset = auto; negative = disabled. | [Routing: Load Shedding and Stats Polling](routing.md#load-shedding-and-stats-polling) |
| `OPENSEARCH_GO_OVERLOADED_HEAP_THRESHOLD` | Integer (0-100) | `85` | JVM heap percentage at or above which a node is marked overloaded (comparison is `>=`). | [Routing: Load Shedding and Stats Polling](routing.md#load-shedding-and-stats-polling) |
| `OPENSEARCH_GO_OVERLOADED_BREAKER_RATIO` | Float (0.0-1.0] | `0.90` | Circuit-breaker `estimated_size / limit_size` ratio at or above which a node is marked overloaded (comparison is `>=`). Values outside `(0.0, 1.0]` are ignored. | [Routing: Load Shedding and Stats Polling](routing.md#load-shedding-and-stats-polling) |

## Connection pool tuning

| Variable | Accepted values | Default | Meaning | See also |
| --- | --- | --- | --- | --- |
| `OPENSEARCH_GO_ACTIVE_LIST_CAP` | Integer | auto | Max active connections per pool. `0` or unset = auto-scale with cluster size. | [Routing: Connection Pool Lifecycle](routing.md#8-connection-pool-lifecycle) |
| `OPENSEARCH_GO_STANDBY_ROTATION_INTERVAL` | Duration or seconds | `0` (use `DiscoverNodesInterval`) | Interval between standby rotation cycles. `0` or unset inherits the node-discovery interval; negative disables rotation. | [Routing: Connection Pool Lifecycle](routing.md#8-connection-pool-lifecycle) |
| `OPENSEARCH_GO_STANDBY_ROTATION_COUNT` | Integer | `1` | Standby connections rotated per cycle. | [Routing: Connection Pool Lifecycle](routing.md#8-connection-pool-lifecycle) |
| `OPENSEARCH_GO_STANDBY_PROMOTION_CHECKS` | Integer | `3` | Consecutive successful health checks required to promote a standby connection to active. | [Routing: Connection Pool Lifecycle](routing.md#8-connection-pool-lifecycle) |

## Debug and diagnostics

| Variable              | Accepted values | Default | Meaning                                                                                                                                                            | See also                                              |
| --------------------- | --------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------- |
| `OPENSEARCH_GO_DEBUG` | Bool            | `false` | Enable verbose internal logging to stderr (routing decisions, discovery, pool operations). Equivalent to setting `EnableDebugLogger: true` in `opensearch.Config`. | [USER_GUIDE.md Debugging](../USER_GUIDE.md#debugging) |

## Error masking

| Variable | Accepted values | Default | Meaning | See also |
| --- | --- | --- | --- | --- |
| `OPENSEARCH_GO_ERROR_MASK` | Comma-separated `+`/`-` tokens | version-dependent (v5+: report all) | Mask (ignore) specific categories of partial-failure errors. Tokens applied left-to-right starting from `Config.Errors`. Unrecognized tokens are silently dropped and logged when `OPENSEARCH_GO_DEBUG=true`. | [opensearchapi/README.md Partial Failure Errors](../opensearchapi/README.md#partial-failure-errors); [Error Handling](error_handling.md) |

### `OPENSEARCH_GO_ERROR_MASK` tokens

Each token is the lowercase snake_case form of the corresponding wrapper-schema name. A bare token (no prefix) is treated as `+` (set/mask the bit). A `-` prefix clears/unmasks the bit.

| Token                            | Category masked               | Returned by                                                                                  |
| -------------------------------- | ----------------------------- | -------------------------------------------------------------------------------------------- |
| `bulk_items`                     | `BulkItems`                   | `Bulk`, `BulkStream`                                                                         |
| `search_shards`                  | `SearchShards`                | `Search`, `MSearch`, `MSearchTemplate`, `SearchTemplate`, `Scroll.Get`, `Count`, `CreatePIT` |
| `write_shards`                   | `WriteShards`                 | `Index`, `Document.Create`, `Document.Delete`, `Update`                                      |
| `broadcast_shards`               | `BroadcastShards`             | `Indices.Refresh`, `Indices.Flush`, `Indices.ForceMerge`, and similar broadcast endpoints    |
| `node_failures`                  | `NodeFailures`                | `Cluster.Stats`, `Nodes.Info`, `Nodes.Stats`, `Nodes.Usage`                                  |
| `bulk_by_scroll_failures`        | `BulkByScrollFailures`        | `Reindex`, `UpdateByQuery`, `DeleteByQuery`                                                  |
| `task_failures`                  | `TaskFailures`                | `Tasks.List`, `Tasks.Cancel`                                                                 |
| `multi_search_items`             | `MultiSearchItems`            | `MSearch`, `MSearchTemplate`                                                                 |
| `multi_doc_items`                | `MultiDocItems`               | `MGet`, `MTermvectors`                                                                       |
| `snapshot_create_shard_failures` | `SnapshotCreateShardFailures` | `Snapshot.Create` (when `wait_for_completion=true`)                                          |
| `snapshot_get_shard_failures`    | `SnapshotGetShardFailures`    | `Snapshot.Get`                                                                               |
| `simulate_doc_failures`          | `SimulateDocFailures`         | `Ingest.Simulate`                                                                            |
| `rank_eval_failures`             | `RankEvalFailures`            | `RankEval`                                                                                   |
| `ingestion_shard_failures`       | `IngestionShardFailures`      | `Ingestion.Pause`, `Ingestion.Resume`                                                        |
| `pit_node_failures`              | `PitNodeFailures`             | `GetAllPits`                                                                                 |

Special tokens:

| Token                      | Meaning                                                                                                |
| -------------------------- | ------------------------------------------------------------------------------------------------------ |
| `all`                      | Set every category bit (mask everything)                                                               |
| `empty`, `none`, `unknown` | Clear every category bit (mask nothing — report everything). All three are aliases for the same value. |

Examples:

```bash
# Mask everything except bulk-item errors
export OPENSEARCH_GO_ERROR_MASK="+all,-bulk_items"

# Only mask search-shard failures; report every other category
export OPENSEARCH_GO_ERROR_MASK="search_shards"

# Mask everything (mimics the v4 default)
export OPENSEARCH_GO_ERROR_MASK="all"

# Report everything (the v5+ default)
export OPENSEARCH_GO_ERROR_MASK="none"
```

## Policy overrides

Ten variables let operators disable specific routing policies at startup without code changes. All policies are enabled by default. Set a variable to `false` or `0` to disable all instances of that policy type.

| Variable                               | Policy type       | Meaning                                                    |
| -------------------------------------- | ----------------- | ---------------------------------------------------------- |
| `OPENSEARCH_GO_POLICY_CHAIN`           | PolicyChain       | Controls whether the chain iterates its children           |
| `OPENSEARCH_GO_POLICY_MUX`             | MuxPolicy         | Controls the multiplexer that fans-out routes by pool name |
| `OPENSEARCH_GO_POLICY_IFENABLED`       | IfEnabledPolicy   | Controls conditional policy branching                      |
| `OPENSEARCH_GO_POLICY_ROUTER`          | poolRouter        | Controls connection-scoring routing                        |
| `OPENSEARCH_GO_POLICY_ROLE`            | RolePolicy        | Controls role-filtered node selection                      |
| `OPENSEARCH_GO_POLICY_ROUNDROBIN`      | RoundRobinPolicy  | Controls round-robin fallback selection                    |
| `OPENSEARCH_GO_POLICY_COORDINATOR`     | CoordinatorPolicy | Controls coordinating-only node routing                    |
| `OPENSEARCH_GO_POLICY_NULL`            | NullPolicy        | Controls the terminal no-op policy                         |
| `OPENSEARCH_GO_POLICY_INDEX_ROUTER`    | IndexRouter       | Controls per-index fan-out routing                         |
| `OPENSEARCH_GO_POLICY_DOCUMENT_ROUTER` | DocRouter         | Controls document-ID-based shard targeting                 |

### Value format

**Bool (disable all instances):**

```bash
OPENSEARCH_GO_POLICY_ROLE=false      # fall through to round-robin
OPENSEARCH_GO_POLICY_ROUTER=false    # plain role-based routing, no scoring
```

Setting `true` is a no-op (same as unset — all policies are enabled by default).

**Path matchers (disable specific instances):** comma-separated `path=bool` items. The path is matched first as a regex, then as a string prefix:

```bash
OPENSEARCH_GO_POLICY_ROLE=chain[0].mux[0].role[0]=false
OPENSEARCH_GO_POLICY_ROLE=.*mux.*role.*=false
```

**Fallback:** a value with no `=` is treated as a regex pattern that disables matching policies.

Policy paths and override actions are logged to stderr when `OPENSEARCH_GO_DEBUG=true`. Overrides are applied once at startup and cannot be changed at runtime. See [Routing: Policy Override Variables](routing.md#policy-override-variables) for the full reference.

---

## Build, test, and development

These variables are **not read by the client at runtime**. They configure the test harness and code-generation tooling, and are listed here so the reference is exhaustive. See [DEVELOPER_GUIDE.md](../DEVELOPER_GUIDE.md) for the testing workflow.

| Variable                          | Used by        | Accepted values                         | Default               | Meaning                                                                                         |
| --------------------------------- | -------------- | --------------------------------------- | --------------------- | ----------------------------------------------------------------------------------------------- |
| `OPENSEARCH_URL`                  | client + tests | Comma-separated URL list                | unset                 | Doubles as the test-cluster endpoint. (Also a runtime variable; see [Connection](#connection).) |
| `OPENSEARCH_VERSION`              | test harness   | version string (e.g. `2.1.0`, `latest`) | unset                 | OpenSearch server version under test.                                                           |
| `SECURE_INTEGRATION`              | test harness   | Bool                                    | `true`                | Run integration tests against a TLS + basic-auth cluster; `false` for an insecure cluster.      |
| `OPENSEARCH_NODE_COUNT`           | test harness   | Integer                                 | `1`                   | Expected node count for cluster-readiness checks.                                               |
| `OPENSEARCH_HEAP_SIZE`            | test harness   | size string (e.g. `2g`)                 | `1g`                  | JVM heap per node for the test cluster.                                                         |
| `TEST_PARALLEL`                   | test harness   | Integer                                 | CPU cores / 2 (min 1) | Max parallel test functions (`go test -parallel`).                                              |
| `OPENSEARCH_GO_SKIP_JSON_COMPARE` | test harness   | presence (any value, including empty)   | unset                 | When set, skips request/response JSON comparison. Detected by presence, not a parsed bool.      |
| `OSGEN_SKIP_GIT_CHECK`            | `cmd/osgen`    | Bool                                    | `false`               | Bypass the git-root safety check during code generation.                                        |

---

## Quick reference

| Variable                                  | Default                      | Summary                               |
| ----------------------------------------- | ---------------------------- | ------------------------------------- |
| `OPENSEARCH_URL`                          | unset                        | Seed addresses                        |
| `OPENSEARCH_GO_ROUTER`                    | `true`                       | Auto-construct DefaultRouter          |
| `OPENSEARCH_GO_ROUTING_CONFIG`            | all enabled                  | Shard-exact and adaptive MCSR toggles |
| `OPENSEARCH_GO_SHARD_COST`                | defaults                     | Shard cost multipliers                |
| `OPENSEARCH_GO_SHARD_REQUESTS`            | `true` (5:256)               | Adaptive MCSR bounds                  |
| `OPENSEARCH_GO_DISCOVERY_CONFIG`          | all enabled                  | Discovery call toggles                |
| `OPENSEARCH_GO_FALLBACK`                  | `true`                       | Seed URL fallback                     |
| `OPENSEARCH_GO_REQUEST_TIMEOUT`           | `0` (none)                   | Per-attempt timeout                   |
| `OPENSEARCH_GO_NODE_STATS_INTERVAL`       | auto                         | Stats polling interval                |
| `OPENSEARCH_GO_OVERLOADED_HEAP_THRESHOLD` | `85`                         | JVM heap overload threshold           |
| `OPENSEARCH_GO_OVERLOADED_BREAKER_RATIO`  | `0.90`                       | Breaker ratio overload threshold      |
| `OPENSEARCH_GO_ACTIVE_LIST_CAP`           | auto                         | Max active connections per pool       |
| `OPENSEARCH_GO_STANDBY_ROTATION_INTERVAL` | `0` (use discovery interval) | Standby rotation interval             |
| `OPENSEARCH_GO_STANDBY_ROTATION_COUNT`    | `1`                          | Standby rotations per cycle           |
| `OPENSEARCH_GO_STANDBY_PROMOTION_CHECKS`  | `3`                          | Health checks before promotion        |
| `OPENSEARCH_GO_DEBUG`                     | `false`                      | Debug logging                         |
| `OPENSEARCH_GO_ERROR_MASK`                | report all (v5+)             | Partial-failure category mask         |
| `OPENSEARCH_GO_POLICY_*`                  | all enabled                  | Per-policy disable (10 variables)     |

# Environment Variables

Canonical reference for every `OPENSEARCH_GO_*` runtime environment variable. Each variable is read once at client initialization and is immutable after. Environment variable values override their programmatic configuration equivalents.

## Why so many environment variables?

The client exposes a large number of operational dials — routing behavior, discovery cadence, overload thresholds, connection-pool sizing, partial-failure handling — and every one of them is configurable in code (`opensearch.Config`, `opensearchapi.Config`, and `RouterOption`s). Each environment variable is an **override** for one of those programmatic settings, so an operator can retune a deployment to meet an organization's operational needs without changing source or rebuilding the binary. The variables are not an alternative configuration system; they are deploy-time escape hatches over the in-code defaults.

## Quick reference

Every runtime variable, its default, and a one-line summary. Use this as the table of contents — each variable links to its detailed entry below.

| Variable                                                                      | Default                      | Summary                               |
| ----------------------------------------------------------------------------- | ---------------------------- | ------------------------------------- |
| [`OPENSEARCH_URL`](#connection)                                               | unset                        | Seed addresses                        |
| [`OPENSEARCH_GO_REQUEST_TIMEOUT`](#connection)                                | `0` (none)                   | Per-attempt timeout                   |
| [`OPENSEARCH_GO_DNS_CACHE_REFRESH`](#connection)                              | `60s`                        | Client-side DNS cache refresh         |
| [`OPENSEARCH_GO_DNS_DIAL_TIMEOUT`](#connection)                               | `30s`                        | DNS-cache dialer dial timeout         |
| [`OPENSEARCH_GO_DNS_KEEP_ALIVE`](#connection)                                 | `30s`                        | DNS-cache dialer keep-alive           |
| [`OPENSEARCH_GO_DNS_TIMEOUT`](#connection)                                    | `10s`                        | DNS-cache per-lookup refresh timeout  |
| [`OPENSEARCH_GO_ROUTER`](#routing)                                            | `true`                       | Auto-construct DefaultRouter          |
| [`OPENSEARCH_GO_ROUTING_CONFIG`](#routing)                                    | all enabled                  | Shard-exact and adaptive MCSR toggles |
| [`OPENSEARCH_GO_SHARD_COST`](#routing)                                        | defaults                     | Shard cost multipliers                |
| [`OPENSEARCH_GO_SHARD_REQUESTS`](#routing)                                    | `true` (5:256)               | Adaptive MCSR bounds                  |
| [`OPENSEARCH_GO_DISCOVERY_CONFIG`](#discovery)                                | all enabled                  | Discovery call toggles                |
| [`OPENSEARCH_GO_FALLBACK`](#discovery)                                        | `true`                       | Seed URL fallback                     |
| [`OPENSEARCH_GO_NODE_STATS_INTERVAL`](#load-shedding-and-stats-polling)       | auto                         | Stats polling interval                |
| [`OPENSEARCH_GO_OVERLOADED_HEAP_THRESHOLD`](#load-shedding-and-stats-polling) | `85`                         | JVM heap overload threshold           |
| [`OPENSEARCH_GO_OVERLOADED_BREAKER_RATIO`](#load-shedding-and-stats-polling)  | `0.90`                       | Breaker ratio overload threshold      |
| [`OPENSEARCH_GO_ACTIVE_LIST_CAP`](#connection-pool-tuning)                    | auto                         | Max active connections per pool       |
| [`OPENSEARCH_GO_STANDBY_ROTATION_INTERVAL`](#connection-pool-tuning)          | `0` (use discovery interval) | Standby rotation interval             |
| [`OPENSEARCH_GO_STANDBY_ROTATION_COUNT`](#connection-pool-tuning)             | `1`                          | Standby rotations per cycle           |
| [`OPENSEARCH_GO_STANDBY_PROMOTION_CHECKS`](#connection-pool-tuning)           | `3`                          | Health checks before promotion        |
| [`OPENSEARCH_GO_DEBUG`](#debug-and-diagnostics)                               | `false`                      | Debug logging                         |
| [`OPENSEARCH_GO_ERROR_MASK`](#error-masking)                                  | report all (v5+)             | Partial-failure category mask         |
| [`OPENSEARCH_GO_POLICY_*`](#policy-overrides)                                 | all enabled                  | Per-policy disable (10 variables)     |
| [`OPENSEARCH_GO_POLICY_DUMP`](#finding-the-paths-the-router-dom)              | `false`                      | Dump router policy tree (debug-gated) |

Build, test, and code-generation variables (not read by the client at runtime) are listed under [Build, test, and development](#build-test-and-development).

## Parsing rules

**Boolean variables.** Parsed with Go's `strconv.ParseBool`. Truthy values: `1`, `t`, `T`, `TRUE`, `true`, `True`. Falsy values: `0`, `f`, `F`, `FALSE`, `false`, `False`. Empty, unset, or unparseable values fall back to the documented default.

**Duration variables.** Accept a Go `time.ParseDuration` string (e.g. `500ms`, `2s`, `1m30s`) or a bare integer/float number of seconds.

**The "See also" column links to the guide section that explains the _concept_ the variable tunes. This document owns the value table; the linked section owns the explanation. No concept is described in two places.**

---

## Connection

| Variable | Accepted values | Default | Meaning | See also |
| --- | --- | --- | --- | --- |
| `OPENSEARCH_URL` | Comma-separated URL list (e.g. `https://a:9200,https://b:9200`) | unset | Seed addresses used by `NewClient` when no `Addresses` are set programmatically. | [opensearchapi/README.md Client Creation](../opensearchapi/README.md#client-creation); [Security: Credential Management](config-security.md#credential-management) |
| `OPENSEARCH_GO_DNS_CACHE_REFRESH` | Duration or seconds | `60s` | Client-side DNS cache refresh interval. Resolved addresses are re-resolved on this cadence; if the resolver becomes briefly unreachable, the last-known-good address is served until it recovers, so a transient DNS outage does not fail requests to already-resolved hosts. `0` or unset = default (`60s`); negative = disable caching; positive = explicit interval. Installed only on the built-in transport; a caller-supplied `Transport` is never modified. Because Go's resolver does not expose record TTLs, this is a re-resolution cadence, not a per-record TTL. Overrides `Config.DNSCacheRefresh`. | [opensearchapi/README.md Client Creation](../opensearchapi/README.md#client-creation) |
| `OPENSEARCH_GO_DNS_DIAL_TIMEOUT` | Duration or seconds | `30s` | Dial timeout of the `net.Dialer` behind the DNS cache. `0` or unset = default (`30s`); negative = no dial timeout; positive = explicit timeout. Only applies when the cache is installed (no custom `Transport`). Overrides `Config.DNSDialTimeout`. | [opensearchapi/README.md Client Creation](../opensearchapi/README.md#client-creation) |
| `OPENSEARCH_GO_DNS_KEEP_ALIVE` | Duration or seconds | `30s` | Keep-alive interval of the `net.Dialer` behind the DNS cache. `0` or unset = default (`30s`); negative = disable keep-alive probes; positive = explicit interval. Only applies when the cache is installed (no custom `Transport`). Overrides `Config.DNSKeepAlive`. | [opensearchapi/README.md Client Creation](../opensearchapi/README.md#client-creation) |
| `OPENSEARCH_GO_DNS_TIMEOUT` | Duration or seconds | `10s` | Per-lookup timeout applied to each DNS cache refresh resolution. Refresh lookups run sequentially on a single goroutine, so this bounds how long one stuck resolution can stall a refresh tick. `0` or unset = default (`10s`); negative = no per-lookup timeout; positive = explicit timeout. Only applies when the cache is installed (no custom `Transport`). Overrides `Config.DNSTimeout`. | [opensearchapi/README.md Client Creation](../opensearchapi/README.md#client-creation) |

## Routing

| Variable | Accepted values | Default | Meaning | See also |
| --- | --- | --- | --- | --- |
| `OPENSEARCH_GO_ROUTER` | Bool | `true` (router auto-constructed) | Auto-construct `NewDefaultRouter()` when no programmatic `Config.Router` is set. Set `false` or `0` to suppress. A programmatic `Config.Router` always takes precedence. | [Routing: Quick Start](transport-routing.md#quick-start); [opensearchapi/README.md Default Router Injection](../opensearchapi/README.md#default-router-injection) |
| `OPENSEARCH_GO_ROUTING_CONFIG` | Comma-separated flags/key=value | all enabled | Toggle shard-exact routing (`-shard_exact`) and adaptive MCSR (`-adaptive_mcsr`). | [Routing: Configuration Reference](transport-routing.md#routing-and-discovery) |
| `OPENSEARCH_GO_SHARD_COST` | Key=value, comma-separated (or bare numeric for `r:base`) | compile-time defaults | Override shard cost multipliers used in connection scoring. Format: key=value pairs (`r:base=0.95`, `unknown=32.0`) or a bare number (sets `r:base`). | [Routing: Shard Cost Configuration](transport-routing.md#shard-cost-configuration) |
| `OPENSEARCH_GO_SHARD_REQUESTS` | Bool or `min:max` | `true` (5:256) | Adaptive `max_concurrent_shard_requests` bounds. `true`/`false` enable/disable with defaults; `10:512`, `10:`, `:512`, or bare `10` set custom bounds. | [Routing: Configuration Reference](transport-routing.md#routing-and-discovery) |

## Discovery

| Variable                         | Accepted values       | Default     | Meaning                                                                                                       | See also                                                                       |
| -------------------------------- | --------------------- | ----------- | ------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| `OPENSEARCH_GO_DISCOVERY_CONFIG` | Comma-separated flags | all enabled | Skip specific discovery calls. Flags: `-cat_shards`, `-routing_num_shards`, `-cluster_health`, `-node_stats`. | [Routing: Configuration Reference](transport-routing.md#routing-and-discovery) |
| `OPENSEARCH_GO_FALLBACK`         | Bool                  | `true`      | Seed URL fallback when all router pools are exhausted. Set `false` to disable.                                | [Routing: Configuration Reference](transport-routing.md#routing-and-discovery) |

## Load shedding and stats polling

| Variable | Accepted values | Default | Meaning | See also |
| --- | --- | --- | --- | --- |
| `OPENSEARCH_GO_REQUEST_TIMEOUT` | Duration or seconds | `0` (none) | Per-attempt HTTP round-trip timeout. | [Security: Connection Timeouts](config-security.md#connection-timeouts) |
| `OPENSEARCH_GO_NODE_STATS_INTERVAL` | Duration or seconds | auto (5s-30s) | Stats polling interval. `0` or unset = auto; negative = disabled. | [Routing: Load Shedding and Stats Polling](transport-routing.md#load-shedding-and-stats-polling) |
| `OPENSEARCH_GO_OVERLOADED_HEAP_THRESHOLD` | Integer (0-100) | `85` | JVM heap percentage at or above which a node is marked overloaded (comparison is `>=`). | [Routing: Load Shedding and Stats Polling](transport-routing.md#load-shedding-and-stats-polling) |
| `OPENSEARCH_GO_OVERLOADED_BREAKER_RATIO` | Float (0.0-1.0] | `0.90` | Circuit-breaker `estimated_size / limit_size` ratio at or above which a node is marked overloaded (comparison is `>=`). Values outside `(0.0, 1.0]` are ignored. | [Routing: Load Shedding and Stats Polling](transport-routing.md#load-shedding-and-stats-polling) |

## Connection pool tuning

| Variable | Accepted values | Default | Meaning | See also |
| --- | --- | --- | --- | --- |
| `OPENSEARCH_GO_ACTIVE_LIST_CAP` | Integer | auto | Max active connections per pool. `0` or unset = auto-scale with cluster size. | [Routing: Connection Pool Lifecycle](transport-routing.md#8-connection-pool-lifecycle) |
| `OPENSEARCH_GO_STANDBY_ROTATION_INTERVAL` | Duration or seconds | `0` (use `DiscoverNodesInterval`) | Interval between standby rotation cycles. `0` or unset inherits the node-discovery interval; negative disables rotation. | [Routing: Connection Pool Lifecycle](transport-routing.md#8-connection-pool-lifecycle) |
| `OPENSEARCH_GO_STANDBY_ROTATION_COUNT` | Integer | `1` | Standby connections rotated per cycle. | [Routing: Connection Pool Lifecycle](transport-routing.md#8-connection-pool-lifecycle) |
| `OPENSEARCH_GO_STANDBY_PROMOTION_CHECKS` | Integer | `3` | Consecutive successful health checks required to promote a standby connection to active. | [Routing: Connection Pool Lifecycle](transport-routing.md#8-connection-pool-lifecycle) |

## Debug and diagnostics

| Variable              | Accepted values | Default | Meaning                                                                                                                                                            | See also                                              |
| --------------------- | --------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------- |
| `OPENSEARCH_GO_DEBUG` | Bool            | `false` | Enable verbose internal logging to stderr (routing decisions, discovery, pool operations). Equivalent to setting `EnableDebugLogger: true` in `opensearch.Config`. | [USER_GUIDE.md Debugging](../USER_GUIDE.md#debugging) |

## Error masking

| Variable | Accepted values | Default | Meaning | See also |
| --- | --- | --- | --- | --- |
| `OPENSEARCH_GO_ERROR_MASK` | Comma-separated `+`/`-` tokens | version-dependent (v5+: report all) | Mask (ignore) specific categories of partial-failure errors. Tokens applied left-to-right starting from `Config.Errors`. Unrecognized tokens are silently dropped and logged when `OPENSEARCH_GO_DEBUG=true`. | [opensearchapi/README.md Partial Failure Errors](../opensearchapi/README.md#partial-failure-errors); [Error Handling](usage-error_handling.md) |

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

Ten variables let operators disable specific routing policies at startup without code changes. All policies are enabled by default. Set a variable to `false` or `0` to disable all instances of that policy type. Each policy type links to its `godoc`.

| Variable                               | Policy type                                                                                                                    | Meaning                                                    |
| -------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------- |
| `OPENSEARCH_GO_POLICY_CHAIN`           | [`PolicyChain`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#PolicyChain)             | Controls whether the chain iterates its children           |
| `OPENSEARCH_GO_POLICY_MUX`             | [`MuxPolicy`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#MuxPolicy)                 | Controls the multiplexer that fans-out routes by pool name |
| `OPENSEARCH_GO_POLICY_IFENABLED`       | [`IfEnabledPolicy`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#IfEnabledPolicy)     | Controls conditional policy branching                      |
| `OPENSEARCH_GO_POLICY_ROUTER`          | `poolRouter` (unexported)                                                                                                      | Controls connection-scoring routing                        |
| `OPENSEARCH_GO_POLICY_ROLE`            | [`RolePolicy`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#RolePolicy)               | Controls role-filtered node selection                      |
| `OPENSEARCH_GO_POLICY_ROUNDROBIN`      | [`RoundRobinPolicy`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#RoundRobinPolicy)   | Controls round-robin fallback selection                    |
| `OPENSEARCH_GO_POLICY_COORDINATOR`     | [`CoordinatorPolicy`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#CoordinatorPolicy) | Controls coordinating-only node routing                    |
| `OPENSEARCH_GO_POLICY_NULL`            | [`NullPolicy`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#NullPolicy)               | Controls the terminal no-op policy                         |
| `OPENSEARCH_GO_POLICY_INDEX_ROUTER`    | [`IndexRouter`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#IndexRouter)             | Controls per-index fan-out routing                         |
| `OPENSEARCH_GO_POLICY_DOCUMENT_ROUTER` | [`DocRouter`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#DocRouter)                 | Controls document-ID-based shard targeting                 |

`OPENSEARCH_GO_POLICY_ROUTER` targets the unexported `poolRouter` type, which has no `godoc` page; it is the connection-scoring wrapper around each role policy.

### Value format

**Bool (disable all instances):**

```bash
OPENSEARCH_GO_POLICY_ROLE=false      # fall through to round-robin
OPENSEARCH_GO_POLICY_ROUTER=false    # plain role-based routing, no scoring
```

Setting `true` is a no-op (same as unset — all policies are enabled by default).

Overrides are applied once at startup and cannot be changed at runtime. See [Routing: Policy Override Variables](transport-routing.md#policy-override-variables) for the full reference.

## Targeting specific policy instances (path matchers)

Instead of a bare bool, an `OPENSEARCH_GO_POLICY_*` value may be a comma-separated list of `path=bool` items that target individual nodes in the router's policy tree. The path is matched first as a regular expression, then (if that does not compile or match) as a string prefix:

```bash
# Disable one exact node: the search-pool router's search-role branch
# (see the labeled tree below to find this path)
OPENSEARCH_GO_POLICY_ROUTER=ifenabled[0].chain[0].mux[0].router[8]=false

# Disable every role node under any router via regex
OPENSEARCH_GO_POLICY_ROLE=.*router.*role.*=false
```

A value with no `=` is treated as a regex pattern that disables matching nodes (the `=false` is implied).

### Finding the paths: the router "DOM"

Path matchers target the dot-delimited node paths the client assigns when it walks the policy tree. There is no public API that returns this tree, so to discover the paths for **your** router, set `OPENSEARCH_GO_POLICY_DUMP=true` **together with** `OPENSEARCH_GO_DEBUG=true` and read the dump from stderr at client initialization:

```bash
OPENSEARCH_GO_DEBUG=true OPENSEARCH_GO_POLICY_DUMP=true ./your-app
```

`OPENSEARCH_GO_POLICY_DUMP` writes through the debug logger, so it produces output only when `OPENSEARCH_GO_DEBUG` is also truthy. It does not change routing behavior; it only prints the tree.

For reference, the **default router** (the tree built by [`NewDefaultRouter`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#NewDefaultRouter) when `OPENSEARCH_GO_ROUTER` is on) produces this 58-node tree. Each `router` node is labeled with the thread pool it scores for, and each `role` node with the role it selects, because the bare path (`router[0]` vs `router[8]`) does not say which pool or role a node serves. Your tree may differ if you supply a custom router or `RouterOption`s:

```text
Router policy tree (58 nodes); target these paths with OPENSEARCH_GO_POLICY_*:
  ifenabled[0]
  ifenabled[0].chain[0]
  ifenabled[0].chain[0].mux[0]
  ifenabled[0].chain[0].mux[0].router[0]  pool=flush
  ifenabled[0].chain[0].mux[0].router[0].ifenabled[0]
  ifenabled[0].chain[0].mux[0].router[0].ifenabled[0].null[0]
  ifenabled[0].chain[0].mux[0].router[0].ifenabled[0].role[0]  role=data
  ifenabled[0].chain[0].mux[0].router[1]  pool=force_merge
  ifenabled[0].chain[0].mux[0].router[1].ifenabled[0]
  ifenabled[0].chain[0].mux[0].router[1].ifenabled[0].null[0]
  ifenabled[0].chain[0].mux[0].router[1].ifenabled[0].role[0]  role=data
  ifenabled[0].chain[0].mux[0].router[2]  pool=get
  ifenabled[0].chain[0].mux[0].router[2].ifenabled[0]
  ifenabled[0].chain[0].mux[0].router[2].ifenabled[0].ifenabled[0]
  ifenabled[0].chain[0].mux[0].router[2].ifenabled[0].ifenabled[0].null[0]
  ifenabled[0].chain[0].mux[0].router[2].ifenabled[0].ifenabled[0].role[0]  role=data
  ifenabled[0].chain[0].mux[0].router[2].ifenabled[0].role[0]  role=search
  ifenabled[0].chain[0].mux[0].router[3]  pool=management
  ifenabled[0].chain[0].mux[0].router[3].ifenabled[0]
  ifenabled[0].chain[0].mux[0].router[3].ifenabled[0].null[0]
  ifenabled[0].chain[0].mux[0].router[3].ifenabled[0].role[0]  role=data
  ifenabled[0].chain[0].mux[0].router[4]  pool=management
  ifenabled[0].chain[0].mux[0].router[4].ifenabled[0]
  ifenabled[0].chain[0].mux[0].router[4].ifenabled[0].null[0]
  ifenabled[0].chain[0].mux[0].router[4].ifenabled[0].role[0]  role=ingest
  ifenabled[0].chain[0].mux[0].router[5]  pool=management
  ifenabled[0].chain[0].mux[0].router[5].ifenabled[0]
  ifenabled[0].chain[0].mux[0].router[5].ifenabled[0].ifenabled[0]
  ifenabled[0].chain[0].mux[0].router[5].ifenabled[0].ifenabled[0].null[0]
  ifenabled[0].chain[0].mux[0].router[5].ifenabled[0].ifenabled[0].role[0]  role=data
  ifenabled[0].chain[0].mux[0].router[5].ifenabled[0].role[0]  role=search
  ifenabled[0].chain[0].mux[0].router[6]  pool=management
  ifenabled[0].chain[0].mux[0].router[6].ifenabled[0]
  ifenabled[0].chain[0].mux[0].router[6].ifenabled[0].ifenabled[0]
  ifenabled[0].chain[0].mux[0].router[6].ifenabled[0].ifenabled[0].null[0]
  ifenabled[0].chain[0].mux[0].router[6].ifenabled[0].ifenabled[0].role[0]  role=data
  ifenabled[0].chain[0].mux[0].router[6].ifenabled[0].role[0]  role=warm
  ifenabled[0].chain[0].mux[0].router[7]  pool=refresh
  ifenabled[0].chain[0].mux[0].router[7].ifenabled[0]
  ifenabled[0].chain[0].mux[0].router[7].ifenabled[0].null[0]
  ifenabled[0].chain[0].mux[0].router[7].ifenabled[0].role[0]  role=data
  ifenabled[0].chain[0].mux[0].router[8]  pool=search
  ifenabled[0].chain[0].mux[0].router[8].ifenabled[0]
  ifenabled[0].chain[0].mux[0].router[8].ifenabled[0].ifenabled[0]
  ifenabled[0].chain[0].mux[0].router[8].ifenabled[0].ifenabled[0].null[0]
  ifenabled[0].chain[0].mux[0].router[8].ifenabled[0].ifenabled[0].role[0]  role=data
  ifenabled[0].chain[0].mux[0].router[8].ifenabled[0].role[0]  role=search
  ifenabled[0].chain[0].mux[0].router[9]  pool=write
  ifenabled[0].chain[0].mux[0].router[9].ifenabled[0]
  ifenabled[0].chain[0].mux[0].router[9].ifenabled[0].null[0]
  ifenabled[0].chain[0].mux[0].router[9].ifenabled[0].role[0]  role=data
  ifenabled[0].chain[0].mux[0].router[10]  pool=write
  ifenabled[0].chain[0].mux[0].router[10].ifenabled[0]
  ifenabled[0].chain[0].mux[0].router[10].ifenabled[0].null[0]
  ifenabled[0].chain[0].mux[0].router[10].ifenabled[0].role[0]  role=ingest
  ifenabled[0].chain[0].roundrobin[0]
  ifenabled[0].router[0]
  ifenabled[0].router[0].role[0]  role=coordinating_only
```

Reading the tree: the `mux[0]` fans requests out to one `router` per (operation, pool) pairing; each router scores connections for its `pool` and delegates to an `ifenabled`/`role` subtree that selects nodes by role (with `null` as the give-up branch). Several routers wrap the same underlying role policy (e.g. every `role=data` leaf), so the same logical policy appears at multiple paths; a path matcher targets the position in the tree, not the shared instance. The labels (`pool=`, `role=`) are not part of the path — match on the path text to the left of the two-space gap.

Node lines are emitted in tree-traversal order (depth-first, siblings ordered as the matcher orders them), so the printed order is the order the override matcher walks. Override actions are also logged to stderr when `OPENSEARCH_GO_DEBUG=true`.

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

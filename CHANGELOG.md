# CHANGELOG

Inspired from [Keep a Changelog](https://keepachangelog.com/en/1.0.0/)

## [Unreleased]

### Added

- Add `InsecureSkipVerify` config option to disable TLS certificate verification without constructing a custom `http.Transport`, preserving `DefaultTransport` connection pooling, HTTP/2, and timeout defaults ([#786](https://github.com/opensearch-project/opensearch-go/issues/786))
- Add `DisableResponseBuffering` config option to skip eager `io.ReadAll` buffering of response bodies in `Perform()`, reducing per-request allocations and TTFB for proxy and streaming use cases ([#786](https://github.com/opensearch-project/opensearch-go/issues/786))
- Add per-attempt `RequestTimeout` to bound individual HTTP round-trips, preventing indefinite hangs on stalled connections ([#786](https://github.com/opensearch-project/opensearch-go/issues/786))
- Add `opensearchutil/shardhash` package with exported `Hash` and `ForRouting` functions for computing OpenSearch shard routing
- Enhanced cluster readiness checking for improved test reliability: `testutil.NewClient()` now includes readiness validation (health + cluster state + nodes info)
- Test parallelization support via TEST_PARALLEL environment variable (default: CPU cores - 1, minimum 1)
- opensearchapi/testutil package with test suite, client helpers, and JSON comparison utilities
- opensearchtransport/testutil package with PollUntil helper for eventual consistency testing (ISM policies, index readiness, cluster state changes)
- Configuration option `IncludeDedicatedClusterManagers` for controlling cluster manager node routing ([#765](https://github.com/opensearch-project/opensearch-go/issues/765))
- Policy-based routing system for improved request routing and service availability ([#771](https://github.com/opensearch-project/opensearch-go/pull/771))
  - `Policy` interface for composable routing strategies with lifecycle management
  - `Router` interface with `Route()` method for request-based connection selection
  - `NewPolicy()` implementing chain-of-responsibility pattern for composable routing strategies
  - `NewIfEnabledPolicy()` for conditional routing with runtime evaluation
  - `NewMuxPolicy()` for trie-based HTTP pattern matching with zero-allocation route lookup
  - `NewRolePolicy()` for role-based node selection
  - `NewRoundRobinRouter()` with coordinating node preference and round-robin fallback
  - `NewMuxRouter()` providing role-based request routing with graceful fallback
    - Automatic routing of bulk, streaming bulk, and reindex operations to ingest nodes
    - Automatic routing of search operations (search, msearch, count, by-query operations, scroll, PIT, validate, rank eval) to search/data nodes
    - Automatic routing of document retrieval operations (get, mget, source, explain, termvectors, mtermvectors) to search/data nodes for read locality
    - Automatic routing of template operations (search template, msearch template) and search shards to search/data nodes
    - Automatic routing of field capabilities to search/data nodes
    - Automatic routing of shard maintenance operations (refresh, flush, synced flush, forcemerge, cache clear, segments) to data nodes
    - Automatic routing of single-document writes (index, create, update, delete) to data nodes
    - Automatic routing of shard diagnostics (recovery, shard stores, stats) and rethrottle operations to data nodes
  - `NewDefaultRouter()` extending role-based routing with per-index node affinity (recommended for most users)
- Add consistent hash routing with per-index node affinity for cache locality and AZ-aware load distribution ([#786](https://github.com/opensearch-project/opensearch-go/pull/786))
  - Rendezvous hashing selects a stable subset of nodes per index, preserving OS page cache and query cache locality
  - RTT-bucketed scoring naturally prefers AZ-local nodes and overflows to remote AZs under load
  - Per-pool congestion window (cwnd) routing using TCP-style AIMD congestion control for capacity-aware connection scoring
  - Thread pool discovery via `/_nodes/_local/http,os,thread_pool` provides per-pool capacity ceiling (maxCwnd)
  - Thread pool stats polling via `/_nodes/_local/stats/jvm,breaker,thread_pool` drives AIMD window adjustments
  - Scoring formula: `RTTBucket * (InFlight + 1) / Cwnd * ShardCostMultiplier` -- nearest node with most headroom wins
  - In-flight request tracking per node per pool with atomic add/release bracketing each RoundTrip
  - AIMD slow start (double cwnd) transitions to congestion avoidance (additive increase) at ssthresh
  - Multiplicative decrease on congestion signals: `total_wait_time_in_nanos` for RESIZABLE pools, queue saturation fallback for others
  - Pool overload detection: `delta(rejected) > 0` or HTTP 429 marks pool overloaded, cleared only by stats poller
  - HTTP 429 handling: TryLock + set overloaded + halve cwnd + retry on different node
  - Quorum gating: pre-quorum uses `4 * defaultServerCoreCount` (= 32) as synthetic cwnd, post-discovery uses `4 * allocatedProcessors`, post-quorum uses real pool cwnd
  - Asymmetric scale-up/scale-down thresholds with hysteresis band for stable active pool sizing
  - Dynamic per-index fan-out driven by shard placement (`/_cat/shards`) and request rate
  - `RouterOption` functional options: `WithMinFanOut`, `WithMaxFanOut`, `WithIndexFanOut`, `WithIdleEvictionTTL`, `WithDecayFactor`, `WithFanOutPerRequest`
- Add environment variable escape hatches (`OPENSEARCH_GO_POLICY_*`) to disable specific routing policies at startup ([#786](https://github.com/opensearch-project/opensearch-go/pull/786))
- Add failure-triggered shard map invalidation for faster routing recovery ([#786](https://github.com/opensearch-project/opensearch-go/pull/786))
  - `lcNeedsCatUpdate` lifecycle bit excludes failed connections from routing candidate sets until `/_cat/shards` refresh
  - Connections remain available for general routing (round-robin, zombie tryouts) while excluded from scored routing
  - Dedicated `discoverCatTimer` schedules lightweight `/_cat/shards`-only refresh (no full node discovery)
  - Refresh urgency scales with cluster impact: `interval = discoverNodesInterval * (1 - flaggedFraction)`, clamped to 5s floor
  - `OnShardMapInvalidation` observer event for monitoring invalidation triggers
  - `NeedsCatUpdate` field in `ConnectionMetric` for observability
- Add routing observability: observer events, metrics snapshot, connection inspection ([#786](https://github.com/opensearch-project/opensearch-go/pull/786))
  - `OnRoute` observer event with full scoring breakdown (`RouteEvent`, `RouteCandidate`)
  - `RouterSnapshot` in `Client.Metrics()` exposes per-index cache state (fan-out, shard nodes, request rate, idle-since)
  - `Connection.RTTMedian()`, `Connection.RTTBucket()`, `Connection.EstLoad()` for per-connection inspection
  - `ConnectionMetric` enriched with `rtt_bucket`, `rtt_median`, `est_load` fields
- Add murmur3 shard-exact routing for `?routing=` and document ID requests ([#786](https://github.com/opensearch-project/opensearch-go/pull/786))
  - Client-side murmur3 x86 32-bit hash matching OpenSearch's `Murmur3HashFunction.hash(String)` for shard-exact targeting
  - Document-level requests (`_doc`, `_source`, `_update`, `_explain`, `_termvectors`) use doc ID as default routing value when no explicit `?routing=` present, matching OpenSearch's `OperationRouting.generateShardId()` behavior
  - Client-side murmur3 shard-exact candidate selection routes requests to nodes hosting the target shard
  - Per-shard-number placement data from `/_cat/shards` maps shard numbers to primary and replica node names
  - Graceful fallback to rendezvous hashing when shard map data is unavailable
  - `RoutingValue`, `EffectiveRoutingKey`, `TargetShard`, `ShardExactMatch` fields in `RouteEvent` for observability
- Add `OPENSEARCH_GO_ROUTING_CONFIG` and `OPENSEARCH_GO_DISCOVERY_CONFIG` environment variables for runtime feature control ([#786](https://github.com/opensearch-project/opensearch-go/pull/786))
  - `OPENSEARCH_GO_ROUTING_CONFIG`: toggle shard-exact routing (`-shard_exact`)
  - `OPENSEARCH_GO_DISCOVERY_CONFIG`: skip individual discovery server calls (`-cat_shards`, `-routing_num_shards`, `-cluster_health`, `-node_stats`)
  - Bitfield flags use `+`/`-` prefix convention for explicit opt-in/out; zero-initialized = all features enabled
  - `WithShardExactRouting(bool)` `RouterOption` for programmatic control (env var overrides)
  - Evaluated once at client init time; immutable after
  - Document environment variables in `guides/routing.md`
  - Document read-after-write visibility guarantees with operation-aware routing in `guides/routing.md`
- Add seed URL fallback as last-resort connection source when all router pools are exhausted ([#786](https://github.com/opensearch-project/opensearch-go/pull/786))
  - Builds a dedicated `multiServerPool` from fresh copies of the original seed URLs at client init
  - Fires after the entire retry loop when all router policies and connection pools return `ErrNoConnections`
  - On success: triggers immediate cluster rediscovery to repopulate router pools
  - `OPENSEARCH_GO_FALLBACK=false` disables seed fallback (enabled by default)
- Add consolidated environment variable reference in `guides/routing.md` and `USER_GUIDE.md` ([#786](https://github.com/opensearch-project/opensearch-go/pull/786))
- Add connection pool health probes with cluster-aware resurrection timing ([#786](https://github.com/opensearch-project/opensearch-go/pull/786))
  - Auto-discover server core count from `/_nodes/http,os,thread_pool` to derive all rate-limiting and congestion window parameters (default: 8 cores)
  - Weighted round-robin for heterogeneous clusters: nodes with more cores get proportionally more traffic via GCD-normalized duplicate pointers in the ready list
  - `lcNeedsHardware` lifecycle bit tracks connections needing hardware info; per-node fallback via `/_nodes/_local/http,os,thread_pool` during health checks
  - Capacity model dynamically recalculated on each discovery cycle from minimum `allocatedProcessors` across all nodes
  - TLS-aware rate limiting prevents overwhelming recovering servers during outages
  - Three-input timeout formula: `max(healthTimeout, rateLimitedTimeout, minimumFloor) + jitter`
  - Shuffle ready connection list on add/resurrect to prevent round-robin hot-spotting
  - Two-phase readiness health check: `GET /` then `GET /_cluster/health?local=true` with `initializing_shards` gate to prevent routing to recovering nodes
  - Store cluster health metrics (`ClusterHealthLocal`) on each connection for observability
  - Periodic cluster health refresh for ready connections keeps `ClusterHealthLocal` data current for load-shedding and routing decisions
    - Refresh interval scales with cluster size: `clamp(liveNodes * clientsPerServer / healthCheckRate, 5s, 5min)`
    - Single-node clusters skip refresh entirely (no routing benefit)
  - Node stats polling with load shedding via `NodeStatsInterval` configuration
    - Polls `GET /_nodes/_local/stats/jvm,breaker,thread_pool` to detect overloaded nodes and update congestion windows
    - Per-pool AIMD congestion control adjusts cwnd based on thread pool wait time and queue saturation
    - Overloaded nodes are demoted from the ready list to the standby partition
    - Overload detection: JVM heap threshold (`OverloadedHeapThreshold`, default 85%), circuit breaker size ratio (`OverloadedBreakerRatio`, default 0.90), breaker trip delta, and cluster status red
- Add heterogeneous Docker cluster targets for integration-testing weighted routing and role-based request routing
  - `cluster.heterogeneous.cpu.1` and `cluster.heterogeneous.cpu.2` set per-node CPU limits via Docker Compose overrides
  - `cluster.heterogeneous.roles` assigns distinct node roles (cluster_manager+ingest, data+ingest, data)
  - `cluster.homogeneous` removes all overrides to reset to default configuration
  - `cluster.status` now shows per-node roles and allocated processors via `_nodes/http,os`
- Add request routing guide (`guides/routing.md`) consolidating routing architecture, connection scoring, pool lifecycle, cost model, and configuration reference ([#786](https://github.com/opensearch-project/opensearch-go/pull/786))

### Changed

- Consolidate test utilities into two canonical packages: opensearchtransport/testutil (env helpers, polling, version comparison) and opensearchapi/testutil (client-dependent helpers, test suite, JSON comparison)
- Rename `singleConnectionPool` to `singleServerPool` and `statusConnectionPool` to `multiServerPool` for clarity ([#786](https://github.com/opensearch-project/opensearch-go/pull/786))
- Refactor Client struct to use embedded mutex pattern for improved thread safety ([#775](https://github.com/opensearch-project/opensearch-go/pull/775))
- Refactor metrics struct to use atomic counters for lock-free request/failure tracking ([#776](https://github.com/opensearch-project/opensearch-go/pull/776))
- Test against Opensearch 2.19.4, 3.1, 3.3, and 3.4 ([#782](https://github.com/opensearch-project/opensearch-go/pull/782))
- Migrate all test files to context-aware API calls for proper timeout and cancellation support
- Add cluster readiness validation and improve cluster error diagnostics
- Update Docker cluster management to add version-aware role detection (cluster_manager vs master)
- Generate unique document IDs in tests for parallel test execution and eliminate known test flakes
- Reduce integration test timeout from 1h to 10m per package with parallel execution support
- Refactor transport code for improved maintainability (rename ErrInvalidRole -> InvalidRoleError, add response body cleanup, simplify initialization)
- **BREAKING**: Enhanced node discovery to match OpenSearch server behavior ([#765](https://github.com/opensearch-project/opensearch-go/issues/765))
  - Dedicated cluster manager nodes are now excluded from client request routing by default (best practice)
  - Node selection logic now matches Java client `NodeSelector.SKIP_DEDICATED_CLUSTER_MASTERS` behavior
- **BREAKING**: Add context support to discovery and client lifecycle management
  - `opensearchtransport.Discoverable` interface now requires `context.Context` parameter: `DiscoverNodes(ctx context.Context) error`
  - `opensearch.Client.DiscoverNodes()` and `opensearchtransport.Client.DiscoverNodes()` now require `context.Context` parameter
  - `opensearch.Config` and `opensearchtransport.Config` now accept optional `Context` and `CancelFunc` fields
  - `opensearchutil.BulkIndexerConfig` now accepts optional `Context` and `CancelFunc` fields
  - Enables proper context propagation for timeouts, cancellation, and graceful shutdown
  - Role compatibility validation prevents conflicting role assignments (master+cluster_manager, warm+search)
  - OpenSearch 3.0+ searchable snapshots now use `warm` role instead of deprecated `search` role
- **BREAKING**: Migrate `signer/aws` package from AWS SDK v1 to AWS SDK v2 due to AWS SDK v1 reaching end-of-support on July 31, 2025
  - Constructor now takes `aws.Config` instead of `session.Options`
  - See USER_GUIDE.md for details required to migrate
  - Users who need access to the existing `signer/awsv2` API can still use it, however they are encouraged to migrate to `signer/aws`

### Deprecated

### Removed

### Fixed

- Fix connection lifecycle bug in multiServerPool.OnFailure where connections were scheduled for resurrection before being moved from ready to dead list, causing potential race conditions
- Fix flaky connection integration test by replacing arbitrary sleep times with proper server readiness polling
- Fix cluster readiness checks in integration tests to handle HTTPS cold start delays (increase timeout to 15s)
- Fix GitHub Actions workflow authentication for OpenSearch 2.12.0+ password changes (admin -> myStrongPassword123!)
- Fix Docker cluster management to properly handle version-specific configurations and clean stale images/volumes
- Fix OpenSearch 2.8.0+ Tasks API compatibility by adding cancellation_time_millis field to TasksListTask struct
- Fix OpenSearch 3.1.0+ API compatibility by adding phase_results_processors field to nodes API and time_in_execution fields to cluster pending tasks API
- Fix OpenSearch 3.2.0+ API compatibility by adding max_last_index_request_timestamp and startree query fields across nodes stats, indices stats, and cat APIs, plus settings field to security plugin health API
- Fix OpenSearch 3.3.0+ API compatibility by adding neural_search breaker, query_failed and startree_query_failed search fields, search pipeline system_generated fields across multiple APIs, plus ingestion_status field to cluster state API and jwks_uri field to security config API
- Fix OpenSearch 3.4.0+ API compatibility by adding warmer fields to merges section, parallelism field to thread pool, and status_counter field across multiple APIs
- Fix cat indices API field naming compatibility across OpenSearch versions by using forward-compatible field names (PrimarySearchStartreeQuery) that match the corrected 3.3.0+ naming, with fallback support for the temporary 3.2.0 field names
- Fix cat APIs data type compatibility by changing byte fields from int to string to properly handle values like "0b"
- Fix floating point precision loss in nodes stats concurrent_avg_slice_count field by changing from float32 to float64
- Fix ISM RefreshSearchAnalyzers missing leading slash in URL path, causing HTTP/2 request failures ([#686](https://github.com/opensearch-project/opensearch-go/pull/686))

### Security

### Dependencies
- Bump `github.com/aws/aws-sdk-go-v2/config` from 1.32.6 to 1.32.12 ([#767](https://github.com/opensearch-project/opensearch-go/pull/767), [#799](https://github.com/opensearch-project/opensearch-go/pull/799))
- Bump `github.com/aws/aws-sdk-go-v2/config` from 1.32.6 to 1.32.7 ([#767](https://github.com/opensearch-project/opensearch-go/pull/767))

## [4.6.0]

### Dependencies

- Bump `github.com/aws/aws-sdk-go-v2/config` from 1.29.14 to 1.32.5 ([#707](https://github.com/opensearch-project/opensearch-go/pull/707), [#711](https://github.com/opensearch-project/opensearch-go/pull/711), [#719](https://github.com/opensearch-project/opensearch-go/pull/719), [#730](https://github.com/opensearch-project/opensearch-go/pull/730), [#737](https://github.com/opensearch-project/opensearch-go/pull/737), [#761](https://github.com/opensearch-project/opensearch-go/pull/761))
- Bump `github.com/aws/aws-sdk-go-v2` from 1.36.4 to 1.41.0 ([#710](https://github.com/opensearch-project/opensearch-go/pull/710), [#720](https://github.com/opensearch-project/opensearch-go/pull/720), [#759](https://github.com/opensearch-project/opensearch-go/pull/759))
- Bump `github.com/stretchr/testify` from 1.10.0 to 1.11.1 ([#728](https://github.com/opensearch-project/opensearch-go/pull/728))
- Bump `github.com/aws/aws-sdk-go` from 1.55.7 to 1.55.8 ([#716](https://github.com/opensearch-project/opensearch-go/pull/716))

### Added

- Adds new fields for Opensearch 3.0 ([#702](https://github.com/opensearch-project/opensearch-go/pull/702))
- Allow users to override signing port ([#721](https://github.com/opensearch-project/opensearch-go/pull/721))
- Add `phase_took` features supported from OpenSearch 2.12 ([#722](https://github.com/opensearch-project/opensearch-go/pull/722))
- Adds the action to refresh the search analyzers to the ISM plugin ([#686](https://github.com/opensearch-project/opensearch-go/pull/686))

### Changed

- Test against Opensearch 3.0 ([#702](https://github.com/opensearch-project/opensearch-go/pull/702))
- Add more SuggestOptions to SearchResp ([#713](https://github.com/opensearch-project/opensearch-go/pull/713))
- Updates Go version to 1.24 ([#674](https://github.com/opensearch-project/opensearch-go/pull/674))
- Replace `golang.org/x/exp/slices` usage with built-in `slices` ([#674](https://github.com/opensearch-project/opensearch-go/pull/674))
- Update golangci-linter to 1.64.8 ([#740](https://github.com/opensearch-project/opensearch-go/pull/740))
- Change MaxScore to pointer ([#740](https://github.com/opensearch-project/opensearch-go/pull/740))
- Update workflow action ([#760](https://github.com/opensearch-project/opensearch-go/pull/760))
- Migrate to golangci-lint v2 ([#760](https://github.com/opensearch-project/opensearch-go/pull/760))

### Deprecated

### Removed

### Fixed

- Missing "caused by" information in StructError ([#752](https://github.com/opensearch-project/opensearch-go/pull/752))
- Add missing `ignore_unavailable`, `allow_no_indices`, and `expand_wildcards` params to MSearch ([#757](https://github.com/opensearch-project/opensearch-go/pull/757))
- Fix `UpdateResp` to correctly parse the `get` field when `_source` is requested in update operations. ([#739](https://github.com/opensearch-project/opensearch-go/pull/739))

### Security

## [4.5.0]

### Dependencies

- Bump `github.com/aws/aws-sdk-go-v2/config` from 1.29.6 to 1.29.14 ([#692](https://github.com/opensearch-project/opensearch-go/pull/692))
- Bump `github.com/aws/aws-sdk-go` from 1.55.6 to 1.55.7 ([#696](https://github.com/opensearch-project/opensearch-go/pull/696))
- Bump `github.com/wI2L/jsondiff` from 0.6.1 to 0.7.0 ([#700](https://github.com/opensearch-project/opensearch-go/pull/700))

### Added

- Adds DataStream field to IndicesGetResp struct ([#701](https://github.com/opensearch-project/opensearch-go/pull/701))
- Adds `InnerHits` field to `SearchResp` ([#672](https://github.com/opensearch-project/opensearch-go/pull/672))
- Adds `FilterPath` param ([#673](https://github.com/opensearch-project/opensearch-go/pull/673))
- Adds `Aggregations` field to `MSearchResp` ([#690](https://github.com/opensearch-project/opensearch-go/pull/690))

### Changed

- Bump golang version to 1.22 ([#691](https://github.com/opensearch-project/opensearch-go/pull/691))
- Change ChangeCatRecoveryItemResp Byte fields from int to string ([#691](https://github.com/opensearch-project/opensearch-go/pull/691))
- Changed log formatted examples code ([#694](https://github.com/opensearch-project/opensearch-go/pull/694))
- Improve the error reporting of invalid body response ([#699](https://github.com/opensearch-project/opensearch-go/pull/699))

### Deprecated

### Removed

### Fixed

### Security

## [4.4.0]

### Added

- Adds `Highlight` field to `SearchHit` ([#654](https://github.com/opensearch-project/opensearch-go/pull/654))
- Adds `MatchedQueries` field to `SearchHit` ([#663](https://github.com/opensearch-project/opensearch-go/pull/663))
- Adds support for Opensearch 2.19 ([#668](https://github.com/opensearch-project/opensearch-go/pull/668))

### Changed

### Deprecated

### Removed

### Fixed

### Security

### Dependencies

- Bump `github.com/aws/aws-sdk-go` from 1.55.5 to 1.55.6 ([#657](https://github.com/opensearch-project/opensearch-go/pull/657))
- Bump `github.com/wI2L/jsondiff` from 0.6.0 to 0.6.1 ([#643](https://github.com/opensearch-project/opensearch-go/pull/643))
- Bump `github.com/aws/aws-sdk-go-v2` from 1.32.2 to 1.36.1 ([#664](https://github.com/opensearch-project/opensearch-go/pull/664))
- Bump `github.com/stretchr/testify` from 1.9.0 to 1.10.0 ([#644](https://github.com/opensearch-project/opensearch-go/pull/644))
- Bump `github.com/aws/aws-sdk-go-v2/config` from 1.27.43 to 1.29.6 ([#665](https://github.com/opensearch-project/opensearch-go/pull/665))

## [4.3.0]

### Added

- Adds ISM Alias action ([#615](https://github.com/opensearch-project/opensearch-go/pull/615))
- Adds support for opensearch 2.17 ([#623](https://github.com/opensearch-project/opensearch-go/pull/623))

### Changed

### Deprecated

### Removed

### Fixed

- Fix ISM Transition to omitempty Conditions field ([#609](https://github.com/opensearch-project/opensearch-go/pull/609))
- Fix ISM Allocation field types ([#609](https://github.com/opensearch-project/opensearch-go/pull/609))
- Fix ISM Error Notification types ([#612](https://github.com/opensearch-project/opensearch-go/pull/612))
- Fix signer receiving drained body on retries ([#620](https://github.com/opensearch-project/opensearch-go/pull/620))
- Fix Bulk Index Items not executing failure callbacks on bulk request failure ([#626](https://github.com/opensearch-project/opensearch-go/issues/626))

### Security

### Dependencies

- Bump `github.com/aws/aws-sdk-go-v2/config` from 1.27.31 to 1.27.43 ([#611](https://github.com/opensearch-project/opensearch-go/pull/611), [#630](https://github.com/opensearch-project/opensearch-go/pull/630), [#632](https://github.com/opensearch-project/opensearch-go/pull/632))
- Bump `github.com/aws/aws-sdk-go-v2` from 1.32.1 to 1.32.2 ([#631](https://github.com/opensearch-project/opensearch-go/pull/631))

## [4.2.0]

### Dependencies

- Bump `github.com/aws/aws-sdk-go-v2/config` from 1.27.23 to 1.27.31 ([#584](https://github.com/opensearch-project/opensearch-go/pull/584), [#588](https://github.com/opensearch-project/opensearch-go/pull/588), [#593](https://github.com/opensearch-project/opensearch-go/pull/593), [#605](https://github.com/opensearch-project/opensearch-go/pull/605))
- Bump `github.com/aws/aws-sdk-go` from 1.54.12 to 1.55.5 ([#583](https://github.com/opensearch-project/opensearch-go/pull/583), [#590](https://github.com/opensearch-project/opensearch-go/pull/590), [#595](https://github.com/opensearch-project/opensearch-go/pull/595), [#596](https://github.com/opensearch-project/opensearch-go/pull/596))

### Added

- Adds `Suggest` to `SearchResp` ([#602](https://github.com/opensearch-project/opensearch-go/pull/602))
- Adds `MaxScore` to `ScrollGetResp` ([#607](https://github.com/opensearch-project/opensearch-go/pull/607))

### Changed

- Split SnapshotGetResp into sub structs ([#603](https://github.com/opensearch-project/opensearch-go/pull/603))

### Deprecated

### Removed

- Remove workflow tests against gotip ([#604](https://github.com/opensearch-project/opensearch-go/pull/604))

### Fixed

### Security

## [4.1.0]

### Added

- Adds the `Routing` field in SearchHit interface. ([#516](https://github.com/opensearch-project/opensearch-go/pull/516))
- Adds the `SearchPipelines` field to `SearchParams` ([#532](https://github.com/opensearch-project/opensearch-go/pull/532))
- Adds support for OpenSearch 2.14 ([#552](https://github.com/opensearch-project/opensearch-go/pull/552))
- Adds the `Caches` field to Node stats ([#572](https://github.com/opensearch-project/opensearch-go/pull/572))
- Adds the `SeqNo` and `PrimaryTerm` fields in `SearchHit` ([#574](https://github.com/opensearch-project/opensearch-go/pull/574))
- Adds guide on configuring the client with retry and backoff ([#540](https://github.com/opensearch-project/opensearch-go/pull/540))
- Adds OpenSearch 2.15 to compatibility workflow test ([#575](https://github.com/opensearch-project/opensearch-go/pull/575))

### Changed

- Security roles get response struct has its own sub structs without omitempty ([#572](https://github.com/opensearch-project/opensearch-go/pull/572))

### Deprecated

### Removed

### Fixed

- Fixes empty request body on retry with compression enabled ([#543](https://github.com/opensearch-project/opensearch-go/pull/543))
- Fixes `Conditions` in `PolicyStateTransition` of ISM plugin ([#556](https://github.com/opensearch-project/opensearch-go/pull/556))
- Fixes integration test response validation when response is null ([#572](https://github.com/opensearch-project/opensearch-go/pull/572))
- Adjust security Role struct for FLS from string to []string ([#572](https://github.com/opensearch-project/opensearch-go/pull/572))
- Fixes wrong response parsing for indices mapping and recovery ([#572](https://github.com/opensearch-project/opensearch-go/pull/572))
- Fixes wrong response parsing for security get requests ([#572](https://github.com/opensearch-project/opensearch-go/pull/572))
- Fixes opensearchtransport ignores request context cancellation when `retryBackoff` is configured ([#540](https://github.com/opensearch-project/opensearch-go/pull/540))
- Fixes opensearchtransport sleeps unexpectedly after the last retry ([#540](https://github.com/opensearch-project/opensearch-go/pull/540))
- Improves ParseError response when server response is an unknown json ([#592](https://github.com/opensearch-project/opensearch-go/pull/592))

### Security

### Dependencies

- Bump `github.com/aws/aws-sdk-go` from 1.51.21 to 1.54.12 ([#534](https://github.com/opensearch-project/opensearch-go/pull/534), [#537](https://github.com/opensearch-project/opensearch-go/pull/537), [#538](https://github.com/opensearch-project/opensearch-go/pull/538), [#545](https://github.com/opensearch-project/opensearch-go/pull/545), [#554](https://github.com/opensearch-project/opensearch-go/pull/554), [#557](https://github.com/opensearch-project/opensearch-go/pull/557), [#563](https://github.com/opensearch-project/opensearch-go/pull/563), [#564](https://github.com/opensearch-project/opensearch-go/pull/564), [#570](https://github.com/opensearch-project/opensearch-go/pull/570), [#579](https://github.com/opensearch-project/opensearch-go/pull/579))
- Bump `github.com/wI2L/jsondiff` from 0.5.1 to 0.6.0 ([#535](https://github.com/opensearch-project/opensearch-go/pull/535), [#566](https://github.com/opensearch-project/opensearch-go/pull/566))
- Bump `github.com/aws/aws-sdk-go-v2/config` from 1.27.11 to 1.27.23 ([#546](https://github.com/opensearch-project/opensearch-go/pull/546), [#553](https://github.com/opensearch-project/opensearch-go/pull/553), [#558](https://github.com/opensearch-project/opensearch-go/pull/558), [#562](https://github.com/opensearch-project/opensearch-go/pull/562), [#567](https://github.com/opensearch-project/opensearch-go/pull/567), [#571](https://github.com/opensearch-project/opensearch-go/pull/571), [#577](https://github.com/opensearch-project/opensearch-go/pull/577))
- Bump `github.com/aws/aws-sdk-go-v2` from 1.27.0 to 1.30.1 ([#559](https://github.com/opensearch-project/opensearch-go/pull/559), [#578](https://github.com/opensearch-project/opensearch-go/pull/578))

## [4.0.0]

### Added

- Adds GlobalIOUsage struct for nodes stats ([#506](https://github.com/opensearch-project/opensearch-go/pull/506))
- Adds the `Explanation` field containing the document explain details to the `SearchHit` struct. ([#504](https://github.com/opensearch-project/opensearch-go/pull/504))
- Adds new error types ([#512](https://github.com/opensearch-project/opensearch-go/pull/506))
- Adds handling of non json errors to ParseError ([#512](https://github.com/opensearch-project/opensearch-go/pull/506))
- Adds the `Failures` field to opensearchapi structs ([#510](https://github.com/opensearch-project/opensearch-go/pull/510))
- Adds the `Fields` field containing the document fields to the `SearchHit` struct. ([#508](https://github.com/opensearch-project/opensearch-go/pull/508))
- Adds security plugin ([#507](https://github.com/opensearch-project/opensearch-go/pull/507))
- Adds security settings to container for security testing ([#507](https://github.com/opensearch-project/opensearch-go/pull/507))
- Adds cluster.get-certs to copy admin certs out of the container ([#507](https://github.com/opensearch-project/opensearch-go/pull/507))
- Adds the `Fields` field containing stored fields to the `DocumentGetResp` struct ([#526](https://github.com/opensearch-project/opensearch-go/pull/526))
- Adds ism plugin ([#524](https://github.com/opensearch-project/opensearch-go/pull/524))

### Changed

- Uses docker compose v2 instead of v1 ([#506](https://github.com/opensearch-project/opensearch-go/pull/506))
- Updates go version to 1.21 ([#509](https://github.com/opensearch-project/opensearch-go/pull/509))
- Moves Error structs from opensearchapi to opensearch package ([#512](https://github.com/opensearch-project/opensearch-go/pull/506))
- Moves parseError function from opensearchapi to opensearch package as ParseError ([#512](https://github.com/opensearch-project/opensearch-go/pull/506))
- Changes ParseError function to do type assertion to determine error type ([#512](https://github.com/opensearch-project/opensearch-go/pull/506))
- Removes unused structs and functions from opensearch ([#517](https://github.com/opensearch-project/opensearch-go/pull/517))
- Adjusts and extent opensearch tests for better coverage ([#517](https://github.com/opensearch-project/opensearch-go/pull/517))
- Bumps codecov action version to v4 ([#517](https://github.com/opensearch-project/opensearch-go/pull/517))
- Changes bulk error/reason field and some cat response fields to pointer as they can be nil ([#510](https://github.com/opensearch-project/opensearch-go/pull/510))
- Adjust workflows to work with security plugin ([#507](https://github.com/opensearch-project/opensearch-go/pull/507))
- Updates USER_GUIDE.md and add samples ([#518](https://github.com/opensearch-project/opensearch-go/pull/518))
- Updates opensearchtransport.Client to use pooled gzip writer and buffer ([#521](https://github.com/opensearch-project/opensearch-go/pull/521))
- Use go:build tags for testing ([#52?](https://github.com/opensearch-project/opensearch-go/pull/52?))

### Deprecated

### Removed

### Fixed

- Fixes search request missing a slash when no indices are given ([#470](https://github.com/opensearch-project/opensearch-go/pull/469))
- Fixes opensearchtransport check for nil response body ([#517](https://github.com/opensearch-project/opensearch-go/pull/517))

### Security

### Dependencies

- Bumps `github.com/aws/aws-sdk-go-v2` from 1.25.3 to 1.26.1
- Bumps `github.com/wI2L/jsondiff` from 0.4.0 to 0.5.1
- Bumps `github.com/aws/aws-sdk-go` from 1.50.36 to 1.51.21
- Bumps `github.com/aws/aws-sdk-go-v2/config` from 1.27.7 to 1.27.11

## [3.1.0]

### Added

- Adds new struct fields introduced in OpenSearch 2.12 ([#482](https://github.com/opensearch-project/opensearch-go/pull/482))
- Adds initial admin password environment variable and CI changes to support 2.12.0 release ([#449](https://github.com/opensearch-project/opensearch-go/pull/449))
- Adds `merge_id` field for indices segment request ([#488](https://github.com/opensearch-project/opensearch-go/pull/488))

### Changed

- Updates workflow action versions ([#488](https://github.com/opensearch-project/opensearch-go/pull/488))
- Changes integration tests to work with secure and unsecure OpenSearch ([#488](https://github.com/opensearch-project/opensearch-go/pull/488))
- Moves functions from `opensearch/internal/test` to `opensearchutil/testutil` for shared test utilities ([#488](https://github.com/opensearch-project/opensearch-go/pull/488))
- Changes `custom_foldername` field to pointer as it can be `null` ([#488](https://github.com/opensearch-project/opensearch-go/pull/488))
- Changs cat indices Primary and Replica field to pointer as it can be `null` ([#488](https://github.com/opensearch-project/opensearch-go/pull/488))
- Replaces `ioutil` with `io` in examples and integration tests [#495](https://github.com/opensearch-project/opensearch-go/pull/495)

### Fixed

- Fix incorrect SigV4 `x-amz-content-sha256` with AWS SDK v1 requests without a body ([#496](https://github.com/opensearch-project/opensearch-go/pull/496))

### Dependencies

- Bumps `github.com/aws/aws-sdk-go` from 1.48.13 to 1.50.36
- Bumps `github.com/aws/aws-sdk-go-v2/config` from 1.25.11 to 1.27.7
- Bumps `github.com/stretchr/testify` from 1.8.4 to 1.9.0

## [3.0.0]

### Added

- Adds `Err()` function to Response for detailed errors ([#246](https://github.com/opensearch-project/opensearch-go/pull/246))
- Adds golangci-lint as code analysis tool ([#313](https://github.com/opensearch-project/opensearch-go/pull/313))
- Adds govulncheck to check for go vulnerablities ([#405](https://github.com/opensearch-project/opensearch-go/pull/405))
- Adds opensearchapi with new client and function structure ([#421](https://github.com/opensearch-project/opensearch-go/pull/421))
- Adds integration tests for all opensearchapi functions ([#421](https://github.com/opensearch-project/opensearch-go/pull/421))
- Adds guide on making raw JSON REST requests ([#399](https://github.com/opensearch-project/opensearch-go/pull/399))
- Adds IPV6 support in the DiscoverNodes method ([#458](https://github.com/opensearch-project/opensearch-go/issues/458))

### Changed

- Removes the need for double error checking ([#246](https://github.com/opensearch-project/opensearch-go/pull/246))
- Updates and adjusted golangci-lint, solve linting complains for signer ([#352](https://github.com/opensearch-project/opensearch-go/pull/352))
- Solves linting complains for opensearchtransport ([#353](https://github.com/opensearch-project/opensearch-go/pull/353))
- Updates Developer guide to include docker build instructions ([#385](https://github.com/opensearch-project/opensearch-go/pull/385))
- Tests against version 2.9.0, 2.10.0, run tests in all branches, changes integration tests to wait for OpenSearch to start ([#392](https://github.com/opensearch-project/opensearch-go/pull/392))
- Makefile: uses docker golangci-lint, run integration test on `.` folder, change coverage generation ([#392](https://github.com/opensearch-project/opensearch-go/pull/392))
- golangci-lint: updates rules and fail when issues are found ([#421](https://github.com/opensearch-project/opensearch-go/pull/421))
- go: updates to golang version 1.20 ([#421](https://github.com/opensearch-project/opensearch-go/pull/421))
- guids: updates to work for the new opensearchapi ([#421](https://github.com/opensearch-project/opensearch-go/pull/421))
- Adjusts tests to new opensearchapi functions and structs ([#421](https://github.com/opensearch-project/opensearch-go/pull/421))
- Changes codecov to comment code coverage to each PR ([#410](https://github.com/opensearch-project/opensearch-go/pull/410))
- Changes module version from v2 to v3 ([#444](https://github.com/opensearch-project/opensearch-go/pull/444))
- Handle unexpected non-json errors with the response body ([#523](https://github.com/opensearch-project/opensearch-go/pull/523))

### Deprecated

- Deprecates legacy API `/_template` ([#390](https://github.com/opensearch-project/opensearch-go/pull/390))

### Removed

- Removes all old opensearchapi functions ([#421](https://github.com/opensearch-project/opensearch-go/pull/421))
- Removes `/internal/build` code and folders ([#421](https://github.com/opensearch-project/opensearch-go/pull/421))

### Fixed

- Corrects AWSv4 signature on DataStream `Stats` with no index name specified ([#338](https://github.com/opensearch-project/opensearch-go/pull/338))
- Fixes GetSourceRequest `Source` field and deprecated the `Source` parameter ([#402](https://github.com/opensearch-project/opensearch-go/pull/402))
- Corrects developer guide summary with golang version 1.20 ([#434](https://github.com/opensearch-project/opensearch-go/pull/434))

### Dependencies

- Bumps `github.com/aws/aws-sdk-go` from 1.44.263 to 1.48.13
- Bumps `github.com/aws/aws-sdk-go-v2` from 1.18.0 to 1.23.5
- Bumps `github.com/aws/aws-sdk-go-v2/config` from 1.18.25 to 1.25.11
- Bumps `github.com/stretchr/testify` from 1.8.2 to 1.8.4
- Bumps `golang.org/x/net` from 0.7.0 to 0.17.0
- Bumps `github.com/golangci/golangci-lint-action` from 1.53.3 to 1.54.2

## [2.3.0]

### Added

- Adds implementation of Data Streams API ([#257](https://github.com/opensearch-project/opensearch-go/pull/257))
- Adds Point In Time API ([#253](https://github.com/opensearch-project/opensearch-go/pull/253))
- Adds InfoResp type ([#253](https://github.com/opensearch-project/opensearch-go/pull/253))
- Adds markdown linter ([#261](https://github.com/opensearch-project/opensearch-go/pull/261))
- Adds testcases to check upsert functionality ([#269](https://github.com/opensearch-project/opensearch-go/pull/269))
- Adds @Jakob3xD to co-maintainers ([#270](https://github.com/opensearch-project/opensearch-go/pull/270))
- Adds dynamic type to \_source field ([#285](https://github.com/opensearch-project/opensearch-go/pull/285))
- Adds testcases for Document API ([#285](https://github.com/opensearch-project/opensearch-go/pull/285))
- Adds `index_lifecycle` guide ([#287](https://github.com/opensearch-project/opensearch-go/pull/287))
- Adds `bulk` guide ([#292](https://github.com/opensearch-project/opensearch-go/pull/292))
- Adds `search` guide ([#291](https://github.com/opensearch-project/opensearch-go/pull/291))
- Adds `document_lifecycle` guide ([#290](https://github.com/opensearch-project/opensearch-go/pull/290))
- Adds `index_template` guide ([#289](https://github.com/opensearch-project/opensearch-go/pull/289))
- Adds `advanced_index_actions` guide ([#288](https://github.com/opensearch-project/opensearch-go/pull/288))
- Adds testcases to check UpdateByQuery functionality ([#304](https://github.com/opensearch-project/opensearch-go/pull/304))
- Adds additional timeout after cluster start ([#303](https://github.com/opensearch-project/opensearch-go/pull/303))
- Adds docker healthcheck to auto restart the container ([#315](https://github.com/opensearch-project/opensearch-go/pull/315))

### Changed

- Uses `[]string` instead of `string` in `SnapshotDeleteRequest` ([#237](https://github.com/opensearch-project/opensearch-go/pull/237))
- Updates workflows to reduce CI time, consolidate OpenSearch versions, update compatibility matrix ([#242](https://github.com/opensearch-project/opensearch-go/pull/242))
- Moves @svencowart to emeritus maintainers ([#270](https://github.com/opensearch-project/opensearch-go/pull/270))
- Reads, closes and replaces the http Response Body ([#300](https://github.com/opensearch-project/opensearch-go/pull/300))

### Fixed

- Corrects curl logging to emit the correct URL destination ([#101](https://github.com/opensearch-project/opensearch-go/pull/101))

### Dependencies

- Bumps `github.com/aws/aws-sdk-go` from 1.44.180 to 1.44.263
- Bumps `github.com/aws/aws-sdk-go-v2` from 1.17.4 to 1.18.0
- Bumps `github.com/aws/aws-sdk-go-v2/config` from 1.18.8 to 1.18.25
- Bumps `github.com/stretchr/testify` from 1.8.1 to 1.8.2

## [2.2.0]

### Added

- Adds Github workflow for changelog verification ([#172](https://github.com/opensearch-project/opensearch-go/pull/172))
- Adds Go Documentation link for the client ([#182](https://github.com/opensearch-project/opensearch-go/pull/182))
- Adds support for Amazon OpenSearch Serverless ([#216](https://github.com/opensearch-project/opensearch-go/pull/216))

### Removed

- Removes info call before performing every request ([#219](https://github.com/opensearch-project/opensearch-go/pull/219))

### Fixed

- Renames the sequence number struct tag to if_seq_no to fix optimistic concurrency control ([#166](https://github.com/opensearch-project/opensearch-go/pull/166))
- Fixes `RetryOnConflict` on bulk indexer ([#215](https://github.com/opensearch-project/opensearch-go/pull/215))

### Dependencies

- Bumps `github.com/aws/aws-sdk-go-v2` from 1.17.1 to 1.17.3
- Bumps `github.com/aws/aws-sdk-go-v2/config` from 1.17.10 to 1.18.8
- Bumps `github.com/aws/aws-sdk-go` from 1.44.176 to 1.44.180
- Bumps `github.com/aws/aws-sdk-go` from 1.44.132 to 1.44.180
- Bumps `github.com/stretchr/testify` from 1.8.0 to 1.8.1
- Bumps `github.com/aws/aws-sdk-go` from 1.44.45 to 1.44.132

[Unreleased]: https://github.com/opensearch-project/opensearch-go/compare/v4.6.0...HEAD
[4.6.0]: https://github.com/opensearch-project/opensearch-go/compare/v4.5.0...v4.6.0
[4.5.0]: https://github.com/opensearch-project/opensearch-go/compare/v4.4.0...v4.5.0
[4.4.0]: https://github.com/opensearch-project/opensearch-go/compare/v4.3.0...v4.4.0
[4.3.0]: https://github.com/opensearch-project/opensearch-go/compare/v4.2.0...v4.3.0
[4.2.0]: https://github.com/opensearch-project/opensearch-go/compare/v4.1.0...v4.2.0
[4.1.0]: https://github.com/opensearch-project/opensearch-go/compare/v4.0.0...v4.1.0
[4.0.0]: https://github.com/opensearch-project/opensearch-go/compare/v3.1.0...v4.0.0
[3.1.0]: https://github.com/opensearch-project/opensearch-go/compare/v3.0.0...v3.1.0
[3.0.0]: https://github.com/opensearch-project/opensearch-go/compare/v2.3.0...v3.0.0
[2.3.0]: https://github.com/opensearch-project/opensearch-go/compare/v2.2.0...v2.3.0
[2.2.0]: https://github.com/opensearch-project/opensearch-go/compare/v2.1.0...v2.2.0
[2.1.0]: https://github.com/opensearch-project/opensearch-go/compare/v2.0.1...v2.1.0
[2.0.1]: https://github.com/opensearch-project/opensearch-go/compare/v2.0.0...v2.0.1
[2.0.0]: https://github.com/opensearch-project/opensearch-go/compare/v1.1.0...v2.0.0
[1.0.0]: https://github.com/opensearch-project/opensearch-go/compare/v1.0.0...v1.1.0

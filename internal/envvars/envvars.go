// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package envvars holds the canonical names of OPENSEARCH_GO_* environment
// variables consumed by both the opensearch and opensearchtransport packages.
// Centralizing the names here avoids two-way duplication across the import
// boundary (opensearch depends on opensearchtransport, but the transport
// must not import opensearch).
package envvars

import (
	"os"
	"strconv"
	"sync"
	"time"
)

// OpenSearchURL is the comma-separated seed-URL list used by [opensearch.NewClient]
// when no Addresses are configured programmatically.
const OpenSearchURL = "OPENSEARCH_URL"

// Router controls whether the DefaultRouter is created automatically when
// no programmatic Config.Router is set, and whether opensearch.NewClient
// inherits on-start node discovery. Parsed as strconv.ParseBool.
const Router = "OPENSEARCH_GO_ROUTER"

// ShardCost configures shard cost multipliers for connection scoring.
// See [opensearchtransport.WithShardCosts] for the format.
const ShardCost = "OPENSEARCH_GO_SHARD_COST"

// RoutingConfig is a comma-separated bitfield/key-value spec controlling
// request-time routing features (e.g. -shard_exact, -adaptive_mcsr).
const RoutingConfig = "OPENSEARCH_GO_ROUTING_CONFIG"

// DiscoveryConfig is a comma-separated bitfield spec controlling which
// discovery API calls run (e.g. -cat_shards, -node_stats).
const DiscoveryConfig = "OPENSEARCH_GO_DISCOVERY_CONFIG"

// Fallback toggles seed-URL fallback when all router pools are exhausted.
const Fallback = "OPENSEARCH_GO_FALLBACK"

// ShardRequests configures adaptive max_concurrent_shard_requests bounds.
// Format: "true"/"false" or "min:max" (e.g. "10:512").
const ShardRequests = "OPENSEARCH_GO_SHARD_REQUESTS"

// RequestTimeout overrides the per-attempt HTTP round-trip timeout.
const RequestTimeout = "OPENSEARCH_GO_REQUEST_TIMEOUT"

// DNSCacheRefresh overrides the client-side DNS cache refresh interval, which
// also bounds how long a stale (last-known-good) address is served when the
// resolver is briefly unreachable. time.ParseDuration format, integer seconds,
// or float seconds. 0 = default, <0 = disable caching, >0 = explicit interval.
const DNSCacheRefresh = "OPENSEARCH_GO_DNS_CACHE_REFRESH"

// DNSDialTimeout overrides the dial timeout of the net.Dialer behind the
// client-side DNS cache. Same value format as DNSCacheRefresh.
// 0 = default (30s), <0 = no dial timeout, >0 = explicit timeout.
const DNSDialTimeout = "OPENSEARCH_GO_DNS_DIAL_TIMEOUT"

// DNSKeepAlive overrides the keep-alive interval of the net.Dialer behind the
// client-side DNS cache. Same value format as DNSCacheRefresh.
// 0 = default (30s), <0 = disable keep-alive probes, >0 = explicit interval.
const DNSKeepAlive = "OPENSEARCH_GO_DNS_KEEP_ALIVE"

// DNSTimeout overrides the per-lookup timeout applied to each cache refresh
// resolution. Same value format as DNSCacheRefresh.
// 0 = default (10s), <0 = no per-lookup timeout, >0 = explicit timeout.
const DNSTimeout = "OPENSEARCH_GO_DNS_TIMEOUT"

// NodeStatsInterval overrides the node stats polling interval.
const NodeStatsInterval = "OPENSEARCH_GO_NODE_STATS_INTERVAL"

// OverloadedHeapThreshold overrides the JVM heap percentage threshold for
// marking a node overloaded.
const OverloadedHeapThreshold = "OPENSEARCH_GO_OVERLOADED_HEAP_THRESHOLD"

// OverloadedBreakerRatio overrides the circuit-breaker ratio threshold for
// marking a node overloaded.
const OverloadedBreakerRatio = "OPENSEARCH_GO_OVERLOADED_BREAKER_RATIO"

// ActiveListCap overrides the per-pool active-connection cap.
const ActiveListCap = "OPENSEARCH_GO_ACTIVE_LIST_CAP"

// StandbyRotationInterval overrides the interval between standby rotation cycles.
const StandbyRotationInterval = "OPENSEARCH_GO_STANDBY_ROTATION_INTERVAL"

// StandbyRotationCount overrides the number of standby rotations performed
// per cycle.
const StandbyRotationCount = "OPENSEARCH_GO_STANDBY_ROTATION_COUNT"

// StandbyPromotionChecks overrides the number of consecutive successful
// health checks required to promote a standby connection to active.
const StandbyPromotionChecks = "OPENSEARCH_GO_STANDBY_PROMOTION_CHECKS"

// Debug enables verbose internal logging via [strconv.ParseBool].
const Debug = "OPENSEARCH_GO_DEBUG"

// PolicyDump dumps the router's policy tree (the structural "DOM": the
// dot-delimited node paths used by OPENSEARCH_GO_POLICY_* path matchers) at
// client initialization when set to a truthy value. The dump is written
// through the debug logger, so it is only emitted when [Debug] is also
// enabled. Parsed via [strconv.ParseBool]. It is not an OPENSEARCH_GO_POLICY_*
// override: the override parser only reads the fixed set of policy type names.
const PolicyDump = "OPENSEARCH_GO_POLICY_DUMP"

// ErrorMask is a comma-separated bitfield spec controlling which categories
// of partial-failure errors API methods mask (ignore) instead of returning
// as typed Go errors. Tokens are the lowercase snake_case wrapper-schema
// names (`bulk_items`, `search_shards`, `write_shards`, `broadcast_shards`,
// `node_failures`, `bulk_by_scroll_failures`, `task_failures`,
// `multi_search_items`, `multi_doc_items`, `snapshot_create_shard_failures`,
// `snapshot_get_shard_failures`, `simulate_doc_failures`,
// `rank_eval_failures`, `ingestion_shard_failures`, `pit_node_failures`)
// plus `all` and `none`/`empty`/`unknown` (aliases for zero). Each token
// may be prefixed with `+` (set/mask) or `-` (clear/unmask); bare tokens
// are treated as `+`. Tokens are applied left-to-right starting from the
// programmatic Config.Errors value. Unrecognized tokens are silently
// dropped (forward-compatible) and reported via the debug logger when
// OPENSEARCH_GO_DEBUG=true.
const ErrorMask = "OPENSEARCH_GO_ERROR_MASK"

// DefaultClientTTL sets the idle eviction window for the process-wide cache of
// implicit default clients. time.ParseDuration format. Unset/invalid = 6m.
// Any negative value disables the cache (every call builds a fresh client).
// 0 means never evict (entries live until process exit). Read once per process.
const DefaultClientTTL = "OPENSEARCH_GO_DEFAULT_CLIENT_TTL"

const defaultClientTTLDefault = 6 * time.Minute

var (
	defaultClientTTLOnce     sync.Once
	defaultClientTTL         time.Duration
	defaultClientTTLDisabled bool
)

// ParseDefaultClientTTL is the pure parser behind DefaultClientTTLValue,
// exposed for testing. ok reports whether val was set. It returns the idle TTL
// and whether the cache is disabled.
func ParseDefaultClientTTL(val string, ok bool) (time.Duration, bool) {
	if !ok || val == "" {
		return defaultClientTTLDefault, false
	}
	d, err := time.ParseDuration(val)
	if err != nil {
		return defaultClientTTLDefault, false
	}
	if d < 0 {
		return 0, true
	}
	return d, false
}

// DefaultClientTTLValue returns the parsed idle TTL and whether the cache is
// disabled. Cached via sync.Once: the env var is read exactly once per process.
func DefaultClientTTLValue() (time.Duration, bool) {
	defaultClientTTLOnce.Do(func() {
		val, ok := os.LookupEnv(DefaultClientTTL)
		defaultClientTTL, defaultClientTTLDisabled = ParseDefaultClientTTL(val, ok)
	})
	return defaultClientTTL, defaultClientTTLDisabled
}

// Truthy reports whether the named environment variable is set to a
// strconv.ParseBool-truthy value. Empty, unset, unparseable, or falsy
// values all return false.
func Truthy(name string) bool {
	val, ok := os.LookupEnv(name)
	if !ok || val == "" {
		return false
	}
	b, err := strconv.ParseBool(val)
	return err == nil && b
}

// Falsy reports whether the named environment variable is set to a
// strconv.ParseBool-falsy value. Empty, unset, unparseable, or truthy
// values all return false. Use this when "explicitly opted out" needs
// to be distinguished from "unset" (where Truthy alone collapses both
// into false).
func Falsy(name string) bool {
	val, ok := os.LookupEnv(name)
	if !ok || val == "" {
		return false
	}
	b, err := strconv.ParseBool(val)
	return err == nil && !b
}

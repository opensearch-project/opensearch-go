// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/url"
	"strings"
)

// Environment variable names for feature configuration.
const (
	// envRoutingConfig controls request-time routing behavior.
	// Format: comma-separated items. +/- prefixed items toggle bitfield
	// flags; other items are key=value pairs (URL-encoded).
	//
	// Bitfield flags:
	//   -shard_exact   Disable murmur3 shard-exact routing
	//   +shard_exact   Explicitly re-enable (overrides programmatic disable)
	//
	// Example: OPENSEARCH_GO_ROUTING_CONFIG=-shard_exact
	envRoutingConfig = "OPENSEARCH_GO_ROUTING_CONFIG"

	// envFallbackConfig controls whether the client falls back to seed
	// URLs when all router policies and connection pools are exhausted.
	// Parsed as strconv.ParseBool. Default: true (fallback enabled).
	//
	// Example: OPENSEARCH_GO_FALLBACK=false
	envFallbackConfig = "OPENSEARCH_GO_FALLBACK"

	// envDiscoveryConfig controls which server calls are made during
	// the discovery cycle.
	//
	// Bitfield flags:
	//   -cat_shards          Skip GET /_cat/shards
	//   -routing_num_shards  Skip GET /_cluster/state/metadata
	//   -cluster_health      Skip GET /_cluster/health?local=true
	//   -node_stats          Skip GET /_nodes/_local/stats
	//
	// Example: OPENSEARCH_GO_DISCOVERY_CONFIG=-routing_num_shards,-node_stats
	envDiscoveryConfig = "OPENSEARCH_GO_DISCOVERY_CONFIG"
)

// routingFeatures is a bitfield where zero-value means all features are
// enabled. Each bit, when set, disables a specific feature.
type routingFeatures uint32

const (
	// routingSkipShardExact disables murmur3 shard-exact routing.
	// When set, calcSingleKeyCost and calcMultiKeyCost return nil and
	// shard-exact routing is bypassed.
	routingSkipShardExact routingFeatures = 1 << iota
)

// shardExactEnabled returns true when murmur3 shard-exact routing is active.
func (f routingFeatures) shardExactEnabled() bool {
	return f&routingSkipShardExact == 0
}

// routingFlagNames maps flag name strings to their bit constants.
//
//nolint:gochecknoglobals // Package-level constant map for config parsing.
var routingFlagNames = map[string]routingFeatures{
	"shard_exact": routingSkipShardExact,
}

// discoveryFeatures is a bitfield where zero-value means all discovery
// fetches are enabled. Each bit, when set, skips a specific server call.
type discoveryFeatures uint32

const (
	// discoverySkipCatShards disables GET /_cat/shards during discovery.
	// No shard placement data -> no shard-aware partitioning, no shard
	// cost multiplier, no shard-exact routing.
	discoverySkipCatShards discoveryFeatures = 1 << iota

	// discoverySkipRoutingNumShards disables
	// GET /_cluster/state/metadata/{indexes} during discovery.
	// No routing_num_shards -> shard-exact routing falls back to
	// rendezvous hashing.
	discoverySkipRoutingNumShards

	// discoverySkipClusterHealth disables
	// GET /_cluster/health?local=true probes.
	// Health checks fall back to baseline GET / only.
	discoverySkipClusterHealth

	// discoverySkipNodeStats disables
	// GET /_nodes/_local/stats/jvm,breaker,thread_pool polling.
	// No per-pool congestion tracking, no overload detection.
	discoverySkipNodeStats
)

// catShardsEnabled returns true when /_cat/shards fetching is active.
func (f discoveryFeatures) catShardsEnabled() bool {
	return f&discoverySkipCatShards == 0
}

// routingNumShardsEnabled returns true when /_cluster/state/metadata fetching is active.
func (f discoveryFeatures) routingNumShardsEnabled() bool {
	return f&discoverySkipRoutingNumShards == 0
}

// clusterHealthEnabled returns true when /_cluster/health probes are active.
func (f discoveryFeatures) clusterHealthEnabled() bool {
	return f&discoverySkipClusterHealth == 0
}

// nodeStatsEnabled returns true when /_nodes/_local/stats polling is active.
func (f discoveryFeatures) nodeStatsEnabled() bool {
	return f&discoverySkipNodeStats == 0
}

// discoveryFlagNames maps flag name strings to their bit constants.
//
//nolint:gochecknoglobals // Package-level constant map for config parsing.
var discoveryFlagNames = map[string]discoveryFeatures{
	"cat_shards":         discoverySkipCatShards,
	"routing_num_shards": discoverySkipRoutingNumShards,
	"cluster_health":     discoverySkipClusterHealth,
	"node_stats":         discoverySkipNodeStats,
}

// parseConfigItems splits a config string on comma, separating bitfield
// items (+/- prefixed) from key=value items. Bitfield items are returned
// as a map of flag name -> enabled. Key=value items are returned as
// url.Values to support multiple values per key.
//
// Items that start with '+' or '-' are bitfield toggles:
//
//	"-shard_exact" -> bits["shard_exact"] = false
//	"+shard_exact" -> bits["shard_exact"] = true
//
// All other items are treated as URL-encoded key=value pairs:
//
//	"key=value" -> kv["key"] = ["value"]
//
// Empty items (from trailing commas) are silently ignored.
func parseConfigItems(value string) (map[string]bool, url.Values) {
	if value == "" {
		return nil, nil
	}

	var bits map[string]bool
	var kv url.Values

	items := strings.SplitSeq(value, ",")
	for item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		// Bitfield toggle: +flag or -flag.
		if item[0] == '+' || item[0] == '-' {
			if bits == nil {
				bits = make(map[string]bool)
			}
			bits[item[1:]] = item[0] == '+'
			continue
		}

		// Key=value pair. Split on first '=' only.
		k, v, hasEq := strings.Cut(item, "=")
		if !hasEq {
			// Bare key without value --ignore (forward-compatible).
			continue
		}

		// URL-decode both key and value.
		decodedKey, err := url.QueryUnescape(k)
		if err != nil {
			decodedKey = k
		}
		decodedVal, err := url.QueryUnescape(v)
		if err != nil {
			decodedVal = v
		}

		if kv == nil {
			kv = make(url.Values)
		}
		kv.Add(decodedKey, decodedVal)
	}

	return bits, kv
}

// parseRoutingConfig applies parsed OPENSEARCH_GO_ROUTING_CONFIG items
// to the routing feature bitfield.
func parseRoutingConfig(value string) routingFeatures {
	bits, _ := parseConfigItems(value)

	var features routingFeatures

	// Apply bitfield flags.
	for name, enabled := range bits {
		bit, ok := routingFlagNames[name]
		if !ok {
			continue // Unknown flag --ignore for forward compatibility.
		}
		if enabled {
			features &^= bit // Clear the skip bit (re-enable).
		} else {
			features |= bit // Set the skip bit (disable).
		}
	}

	return features
}

// parseDiscoveryConfig applies parsed OPENSEARCH_GO_DISCOVERY_CONFIG items
// to the discovery feature bitfield.
func parseDiscoveryConfig(value string) discoveryFeatures {
	bits, _ := parseConfigItems(value)

	var features discoveryFeatures
	for name, enabled := range bits {
		bit, ok := discoveryFlagNames[name]
		if !ok {
			continue // Unknown flag --ignore for forward compatibility.
		}
		if enabled {
			features &^= bit // Clear the skip bit (re-enable).
		} else {
			features |= bit // Set the skip bit (disable).
		}
	}

	return features
}

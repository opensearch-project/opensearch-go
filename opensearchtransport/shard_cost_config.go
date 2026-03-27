// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"slices"
	"strconv"
	"strings"
)

// ShardCostConfigError is returned when [parseShardCostConfig] encounters
// an invalid shard cost configuration string.
type ShardCostConfigError struct {
	// Key is the config key that caused the error (may be empty for bare values).
	Key string
	// Reason classifies the failure: "unknown key", "invalid value", or "missing value".
	Reason string
	// Detail provides additional context (e.g. valid keys, wrapped parse error).
	Detail string
	// Err is the underlying error, if any (e.g. strconv.ErrSyntax).
	Err error
}

func (e *ShardCostConfigError) Error() string {
	msg := "shard cost config: " + e.Reason
	if e.Key != "" {
		msg += " for key " + strconv.Quote(e.Key)
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *ShardCostConfigError) Unwrap() error { return e.Err }

// envShardCost controls shard cost multiplier values used for connection scoring.
//
// Format: bare float (sets preferred+alternate for both read and write tables)
// or comma-separated items in key=value form with optional r:/w: prefix.
//
// Without prefix, keys use abstract role names that map to the correct shard
// type per table: preferred, alternate, relocating, initializing, unknown.
// "preferred" means replica for reads, primary for writes.
//
// With r: or w: prefix, keys use concrete shard type names for the specified
// table: primary, replica, relocating, initializing, unknown.
//
// Any value <= 0 is replaced by the compile-time default for that slot.
//
// Examples:
//
//	OPENSEARCH_GO_SHARD_COST=1.5                                    # preferred=alternate=1.5 in both tables
//	OPENSEARCH_GO_SHARD_COST=preferred=1.0,alternate=1.0            # disable role preference
//	OPENSEARCH_GO_SHARD_COST=r:replica=1.0,w:primary=0.5,alternate=1  # mix concrete and abstract
const envShardCost = "OPENSEARCH_GO_SHARD_COST"

const (
	// counterFloor is the minimum value used for the decay counter
	// in score calculations. Prevents division-by-zero-like effects when a
	// node has received no recent traffic.
	counterFloor = 1.0

	// Shard cost multipliers used in the [shardCostMultiplier] tables and
	// [warmupPenaltyMax]. Lower value = preferred node.
	costPreferred    = 1.0  // best-case: node hosts the ideal shard type
	costAlternate    = 2.0  // acceptable: node can serve but may proxy
	costRelocating   = 8.0  // shard moving, may require proxy hop
	costInitializing = 16.0 // shard not yet ready to serve
	costUnknown      = 32.0 // no shard data, heavily penalized
)

// shardCostIndex identifies a shard state position in a [shardCostMultiplier].
type shardCostIndex int

const (
	// shardCostUnknown is the zero value: no shard data available.
	// A zero-initialized [shardCostMultiplier] produces 0.0 for unknown,
	// so tables must be explicitly constructed.
	shardCostUnknown shardCostIndex = iota

	// shardCostReplica: node hosts only replica shards for this index.
	shardCostReplica

	// shardCostPrimary: node hosts only primary shards for this index.
	shardCostPrimary

	// shardCostInitializing: node has initializing shards (reserved for
	// future use; discovery currently filters to STARTED shards only).
	shardCostInitializing

	// shardCostRelocating: node has relocating shards (reserved for
	// future use; discovery currently filters to STARTED shards only).
	shardCostRelocating
)

// shardCostMultiplier holds per-shard-state score multipliers used in
// [calcConnScore]. The appropriate table is selected at policy construction
// time based on whether the route handles reads or writes.
//
// Lower multiplier = preferred node. Index via [shardCostIndex] constants.
type shardCostMultiplier [5]float64

// shardCostForReads prefers replica-hosting nodes. Replicas serve reads
// from a lock-free Lucene snapshot that doesn't contend with writes.
//
//nolint:gochecknoglobals // Package-level constant table used by calcConnScore.
var shardCostForReads = shardCostMultiplier{
	shardCostUnknown:      costUnknown,      // no data, heavily penalized
	shardCostReplica:      costPreferred,    // preferred for reads
	shardCostPrimary:      costAlternate,    // primaries contend with writes
	shardCostInitializing: costInitializing, // shard not yet ready
	shardCostRelocating:   costRelocating,   // shard moving, may proxy
}

// shardCostForWrites prefers primary-hosting nodes. Writes always go to
// the primary shard first; routing to a replica-only node forces a
// coordinator proxy hop.
//
//nolint:gochecknoglobals // Package-level constant table used by calcConnScore.
var shardCostForWrites = shardCostMultiplier{
	shardCostUnknown:      costUnknown,      // no data, heavily penalized
	shardCostReplica:      costAlternate,    // replica must proxy to primary
	shardCostPrimary:      costPreferred,    // preferred -- write lands directly
	shardCostInitializing: costInitializing, // shard not yet ready
	shardCostRelocating:   costRelocating,   // shard moving, may proxy
}

// forNode returns the shard cost multiplier for a node based on its shard
// composition for the target index.
//
// The lookup is categorical: if the node hosts the preferred shard type
// (as encoded by the table), it gets the preferred cost. Mixed nodes that
// host both primaries and replicas get the best (lowest) cost since they
// can serve both reads and writes locally. Load-based differentiation
// between nodes is handled by the utilization ratio (inFlight+1)/cwnd, not by this
// multiplier.
func (m *shardCostMultiplier) forNode(node *shardNodeInfo) float64 {
	if node == nil {
		return m[shardCostUnknown]
	}
	total := node.Primaries + node.Replicas
	if total == 0 {
		return m[shardCostUnknown]
	}
	if node.Primaries == 0 {
		return m[shardCostReplica]
	}
	if node.Replicas == 0 {
		return m[shardCostPrimary]
	}
	// Mixed node: hosts both primaries and replicas. It can serve reads
	// from replicas and writes to primaries locally. Use the better cost;
	// the CPU counter differentiates actual load between mixed nodes.
	return min(m[shardCostReplica], m[shardCostPrimary])
}

// forShard returns the shard cost multiplier for a connection based on its
// role for a specific shard. Unlike [forNode] (which uses aggregate per-node
// counts across all shards of an index), this uses the per-shard placement
// data from [shardNodes] to determine the exact role --primary or replica
// --for the target shard resolved via murmur3 hashing.
//
// Used by the shard lookup path when the target shard number is known.
func (m *shardCostMultiplier) forShard(shard *shardNodes, connName string) float64 {
	if shard == nil {
		return m[shardCostUnknown]
	}
	if shard.Primary == connName {
		return m[shardCostPrimary]
	}
	if slices.Contains(shard.Replicas, connName) {
		return m[shardCostReplica]
	}
	return m[shardCostUnknown]
}

// shardCostKeyName maps user-facing key names to the read and write table
// indices they affect. The "preferred"/"alternate" abstraction hides the
// internal replica/primary distinction: for reads, preferred = replica;
// for writes, preferred = primary.
type shardCostKeyName struct {
	readIdx  shardCostIndex
	writeIdx shardCostIndex
}

// shardCostAbstractKeys maps abstract (unprefixed) key names to table indices.
// Used when no r:/w: prefix is present; the mapping accounts for the role
// inversion between read and write tables.
//
//nolint:gochecknoglobals // Package-level constant map for config parsing.
var shardCostAbstractKeys = map[string]shardCostKeyName{
	"preferred":    {readIdx: shardCostReplica, writeIdx: shardCostPrimary},
	"alternate":    {readIdx: shardCostPrimary, writeIdx: shardCostReplica},
	"relocating":   {readIdx: shardCostRelocating, writeIdx: shardCostRelocating},
	"initializing": {readIdx: shardCostInitializing, writeIdx: shardCostInitializing},
	"unknown":      {readIdx: shardCostUnknown, writeIdx: shardCostUnknown},
}

// shardCostConcreteKeys maps concrete (prefixed) key names to a single table
// index. Used with r: or w: prefix where the caller has already selected the
// target table and names the shard type directly (primary, replica).
//
//nolint:gochecknoglobals // Package-level constant map for config parsing.
var shardCostConcreteKeys = map[string]shardCostIndex{
	"primary":      shardCostPrimary,
	"replica":      shardCostReplica,
	"relocating":   shardCostRelocating,
	"initializing": shardCostInitializing,
	"unknown":      shardCostUnknown,
}

// applyShardCostItem applies a single parsed key=value item to the read
// and/or write cost tables based on the prefix ("", "r", or "w").
func applyShardCostItem(
	prefix, key string, v float64,
	reads, writes *shardCostMultiplier,
) error {
	if prefix == "" {
		// Abstract key: apply to both tables with role-aware mapping.
		mapping, ok := shardCostAbstractKeys[key]
		if !ok {
			return &ShardCostConfigError{
				Key:    key,
				Reason: "unknown key",
				Detail: "valid unprefixed keys: preferred, alternate, relocating, initializing, unknown",
			}
		}
		reads[mapping.readIdx] = v
		writes[mapping.writeIdx] = v
		return nil
	}

	// Concrete key: apply to the named table directly.
	idx, ok := shardCostConcreteKeys[key]
	if !ok {
		return &ShardCostConfigError{
			Key:    key,
			Reason: "unknown key",
			Detail: "valid prefixed keys: primary, replica, relocating, initializing, unknown",
		}
	}
	if prefix == "r" {
		reads[idx] = v
	} else {
		writes[idx] = v
	}
	return nil
}

// parseShardCostConfig parses a shard cost configuration string into read
// and write [shardCostMultiplier] tables. Returns copies of the compile-time
// defaults when spec is empty.
//
// Format:
//
//	Bare numeric: "1.5" → sets preferred and alternate to 1.5 for both tables.
//	Unprefixed:   "key=value,..." where keys are preferred, alternate,
//	              relocating, initializing, unknown. Applied to both tables
//	              with role-aware mapping (preferred = replica for reads,
//	              primary for writes).
//	Prefixed:     "r:key=value" or "w:key=value" where keys are primary,
//	              replica, relocating, initializing, unknown. Applied to the
//	              named table with direct index mapping.
//
// Any parsed value <= 0 is replaced by the compile-time default for that slot.
func parseShardCostConfig(spec string) (shardCostMultiplier, shardCostMultiplier, error) {
	reads := shardCostForReads   // value copy
	writes := shardCostForWrites // value copy

	spec = strings.TrimSpace(spec)
	if spec == "" {
		return reads, writes, nil
	}

	// Try bare numeric first.
	if v, parseErr := strconv.ParseFloat(spec, 64); parseErr == nil {
		if v > 0 {
			// Set preferred and alternate in both tables.
			reads[shardCostAbstractKeys["preferred"].readIdx] = v
			reads[shardCostAbstractKeys["alternate"].readIdx] = v
			writes[shardCostAbstractKeys["preferred"].writeIdx] = v
			writes[shardCostAbstractKeys["alternate"].writeIdx] = v
		}
		return reads, writes, nil
	}

	// Key=value parsing.
	for item := range strings.SplitSeq(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		// Detect r: or w: prefix.
		var prefix string
		switch {
		case strings.HasPrefix(item, "r:"):
			prefix = "r"
			item = item[2:]
		case strings.HasPrefix(item, "w:"):
			prefix = "w"
			item = item[2:]
		}

		key, valStr, hasEq := strings.Cut(item, "=")
		if !hasEq {
			return shardCostForReads, shardCostForWrites,
				&ShardCostConfigError{Key: key, Reason: "missing value"}
		}

		v, parseErr := strconv.ParseFloat(valStr, 64)
		if parseErr != nil {
			return shardCostForReads, shardCostForWrites,
				&ShardCostConfigError{Key: key, Reason: "invalid value", Err: parseErr}
		}

		if err := applyShardCostItem(prefix, key, v, &reads, &writes); err != nil {
			return shardCostForReads, shardCostForWrites, err
		}
	}

	// Clamp: replace any value <= 0 with the compile-time default.
	for i := range reads {
		if reads[i] <= 0 {
			reads[i] = shardCostForReads[i]
		}
	}
	for i := range writes {
		if writes[i] <= 0 {
			writes[i] = shardCostForWrites[i]
		}
	}

	return reads, writes, nil
}

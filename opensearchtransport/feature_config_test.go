// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport //nolint:testpackage // tests unexported parseConfigItems, routingFeatures, etc.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseConfigItems_Empty(t *testing.T) {
	t.Parallel()
	bits, kv := parseConfigItems("")
	require.Nil(t, bits)
	require.Nil(t, kv)
}

func TestParseConfigItems_BitfieldOnly(t *testing.T) {
	t.Parallel()
	bits, kv := parseConfigItems("-shard_exact,+prefer_local")
	require.NotNil(t, bits)
	require.False(t, bits["shard_exact"])
	require.True(t, bits["prefer_local"])
	require.Nil(t, kv)
}

func TestParseConfigItems_KVOnly(t *testing.T) {
	t.Parallel()
	bits, kv := parseConfigItems("color=blue,timeout=5s")
	require.Nil(t, bits)
	require.NotNil(t, kv)
	require.Equal(t, "blue", kv.Get("color"))
	require.Equal(t, "5s", kv.Get("timeout"))
}

func TestParseConfigItems_Mixed(t *testing.T) {
	t.Parallel()
	bits, kv := parseConfigItems("-shard_exact,color=blue,+cat_shards")
	require.NotNil(t, bits)
	require.False(t, bits["shard_exact"])
	require.True(t, bits["cat_shards"])
	require.NotNil(t, kv)
	require.Equal(t, "blue", kv.Get("color"))
}

func TestParseConfigItems_URLEncoded(t *testing.T) {
	t.Parallel()
	// key=orders%3Dval -> key=key, value=orders=val
	bits, kv := parseConfigItems("key=orders%3Dval")
	require.Nil(t, bits)
	require.NotNil(t, kv)
	require.Equal(t, "orders=val", kv.Get("key"))
}

func TestParseConfigItems_MultipleValues(t *testing.T) {
	t.Parallel()
	// url.Values supports repeated keys.
	_, kv := parseConfigItems("tag=a,tag=b%3Dc,tag=d")
	require.NotNil(t, kv)
	tags := kv["tag"]
	require.Len(t, tags, 3)
	require.Equal(t, "a", tags[0])
	require.Equal(t, "b=c", tags[1])
	require.Equal(t, "d", tags[2])
}

func TestParseConfigItems_TrailingComma(t *testing.T) {
	t.Parallel()
	bits, _ := parseConfigItems("-shard_exact,")
	require.NotNil(t, bits)
	require.False(t, bits["shard_exact"])
}

func TestParseConfigItems_BareKey(t *testing.T) {
	t.Parallel()
	// Bare key without '=' is ignored (forward-compatible).
	bits, kv := parseConfigItems("unknown_flag")
	require.Nil(t, bits)
	require.Nil(t, kv)
}

// --- routingFeatures bitfield tests ---

func TestRoutingFeatures_ZeroValueAllEnabled(t *testing.T) {
	t.Parallel()
	var f routingFeatures
	require.True(t, f.shardExactEnabled())
}

func TestRoutingFeatures_SkipBit(t *testing.T) {
	t.Parallel()
	f := routingSkipShardExact
	require.False(t, f.shardExactEnabled())
}

// --- discoveryFeatures bitfield tests ---

func TestDiscoveryFeatures_ZeroValueAllEnabled(t *testing.T) {
	t.Parallel()
	var f discoveryFeatures
	require.True(t, f.catShardsEnabled())
	require.True(t, f.routingNumShardsEnabled())
	require.True(t, f.clusterHealthEnabled())
	require.True(t, f.nodeStatsEnabled())
}

func TestDiscoveryFeatures_IndividualSkip(t *testing.T) {
	t.Parallel()
	f := discoverySkipCatShards | discoverySkipNodeStats
	require.False(t, f.catShardsEnabled())
	require.True(t, f.routingNumShardsEnabled())
	require.True(t, f.clusterHealthEnabled())
	require.False(t, f.nodeStatsEnabled())
}

// --- parseRoutingConfig tests ---

func TestParseRoutingConfig_Empty(t *testing.T) {
	t.Parallel()
	features := parseRoutingConfig("")
	require.Equal(t, routingFeatures(0), features)
}

func TestParseRoutingConfig_DisableShardExact(t *testing.T) {
	t.Parallel()
	features := parseRoutingConfig("-shard_exact")
	require.False(t, features.shardExactEnabled())
}

func TestParseRoutingConfig_ReEnableShardExact(t *testing.T) {
	t.Parallel()
	// Disable then re-enable.
	features := parseRoutingConfig("-shard_exact,+shard_exact")
	require.True(t, features.shardExactEnabled())
}

func TestParseRoutingConfig_UnknownFlag(t *testing.T) {
	t.Parallel()
	// Unknown flags are silently ignored for forward compatibility.
	features := parseRoutingConfig("-unknown_flag,-shard_exact")
	require.False(t, features.shardExactEnabled())
}

// --- parseDiscoveryConfig tests ---

func TestParseDiscoveryConfig_Empty(t *testing.T) {
	t.Parallel()
	f := parseDiscoveryConfig("")
	require.True(t, f.catShardsEnabled())
	require.True(t, f.routingNumShardsEnabled())
	require.True(t, f.clusterHealthEnabled())
	require.True(t, f.nodeStatsEnabled())
}

func TestParseDiscoveryConfig_DisableMultiple(t *testing.T) {
	t.Parallel()
	f := parseDiscoveryConfig("-routing_num_shards,-node_stats")
	require.True(t, f.catShardsEnabled())
	require.False(t, f.routingNumShardsEnabled())
	require.True(t, f.clusterHealthEnabled())
	require.False(t, f.nodeStatsEnabled())
}

func TestParseDiscoveryConfig_DisableAll(t *testing.T) {
	t.Parallel()
	f := parseDiscoveryConfig("-cat_shards,-routing_num_shards,-cluster_health,-node_stats")
	require.False(t, f.catShardsEnabled())
	require.False(t, f.routingNumShardsEnabled())
	require.False(t, f.clusterHealthEnabled())
	require.False(t, f.nodeStatsEnabled())
}

func TestParseDiscoveryConfig_ReEnable(t *testing.T) {
	t.Parallel()
	f := parseDiscoveryConfig("-cat_shards,+cat_shards")
	require.True(t, f.catShardsEnabled())
}

func TestParseDiscoveryConfig_UnknownFlag(t *testing.T) {
	t.Parallel()
	f := parseDiscoveryConfig("-future_feature,-cat_shards")
	require.False(t, f.catShardsEnabled())
	require.True(t, f.routingNumShardsEnabled())
}

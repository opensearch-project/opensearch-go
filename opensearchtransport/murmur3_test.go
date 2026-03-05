// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport //nolint:testpackage // requires internal access to murmur3Hash32x86

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Test vectors from OpenSearch server:
// server/src/test/java/org/opensearch/cluster/routing/operation/hash/murmur3/Murmur3HashFunctionTests.java
//
// Java stores these as signed int32 hex literals. In Java, hex int literals
// with bit 31 set are negative (e.g. 0xd7c31989 is a valid int = -675079799).
//
// Source: assertHash(0x..., "...") in Murmur3HashFunctionTests.java
func TestOpensearchShardHash_KnownValues(t *testing.T) {
	// Use a helper to convert uint32 hex -> signed int32, avoiding Go's
	// compile-time overflow checks on int32 hex literals > 0x7fffffff.
	si := func(u uint32) int32 { return int32(u) } //nolint:gosec // intentional uint32->int32 reinterpretation for test hex values

	tests := []struct {
		input    string
		expected int32
	}{
		{"hell", si(0x5a0cb7c3)},                                        // 1510782915
		{"hello", si(0xd7c31989)},                                       // -675079799
		{"hello w", si(0x22ab2984)},                                     // 581642628
		{"hello wo", si(0xdf0ca123)},                                    // -552820445
		{"hello wor", si(0xe7744d61)},                                   // -411808415
		{"The quick brown fox jumps over the lazy dog", si(0xe07db09c)}, // -528633700
		{"The quick brown fox jumps over the lazy cog", si(0x4e63d2ad)}, // 1315164845
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := opensearchShardHash(tt.input)
			require.Equal(t, tt.expected, got, "opensearchShardHash(%q)", tt.input)
		})
	}
}

func TestOpensearchShardHash_EmptyString(t *testing.T) {
	// Empty string should produce a deterministic hash (not panic).
	h := opensearchShardHash("")
	// The hash of empty UTF-16 LE bytes with seed=0 is the finalization
	// of h1=0 ^ length=0 -> fmix32(0).
	require.Equal(t, fmix32(0), h)
}

func TestFloorMod(t *testing.T) {
	tests := []struct {
		a        int32
		b        int
		expected int
	}{
		{0, 4, 0},
		{1, 4, 1},
		{4, 4, 0},
		{5, 4, 1},
		{-1, 4, 3}, // Java: Math.floorMod(-1, 4) == 3
		{-2, 4, 2}, // Java: Math.floorMod(-2, 4) == 2
		{-4, 4, 0}, // Java: Math.floorMod(-4, 4) == 0
		{-5, 4, 3}, // Java: Math.floorMod(-5, 4) == 3
		{-7, 8, 1}, // Java: Math.floorMod(-7, 8) == 1
		{7, 8, 7},
		{1, 1, 0}, // Single shard
	}

	for _, tt := range tests {
		got := floorMod(tt.a, tt.b)
		require.Equal(t, tt.expected, got, "floorMod(%d, %d)", tt.a, tt.b)
	}
}

func TestShardForRouting(t *testing.T) {
	si := func(u uint32) int32 { return int32(u) } //nolint:gosec // intentional uint32->int32 reinterpretation for test hex values

	// Use the actual hash values from TestOpensearchShardHash_KnownValues.
	// "hello" -> int32(0xd7c31989) = -675079799
	// "hell"  -> int32(0x5a0cb7c3) = 1510782915
	helloHash := si(0xd7c31989) // -675079799
	hellHash := si(0x5a0cb7c3)  // 1510782915

	tests := []struct {
		name             string
		routing          string
		routingNumShards int
		numShards        int
		expectedShard    int
	}{
		// routingNumShards == numShards (routingFactor=1): legacy simplified formula
		{"hello/factor1", "hello", 5, 5, floorMod(helloHash, 5)},
		{"hello/single", "hello", 1, 1, 0}, // single shard -> always 0
		{"hello/factor1/8", "hello", 8, 8, floorMod(helloHash, 8)},
		{"hell/factor1", "hell", 5, 5, floorMod(hellHash, 5)},

		// Realistic routingNumShards (from OpenSearch 3.x defaults).
		// 5 shards -> routingNumShards=640, routingFactor=128.
		// shard = floorMod(hash, 640) / 128
		{"hello/5shard_real", "hello", 640, 5, floorMod(helloHash, 640) / 128},
		{"hell/5shard_real", "hell", 640, 5, floorMod(hellHash, 640) / 128},

		// 3 shards -> routingNumShards=768, routingFactor=256.
		{"hello/3shard_real", "hello", 768, 3, floorMod(helloHash, 768) / 256},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shardForRouting(tt.routing, tt.routingNumShards, tt.numShards)
			require.GreaterOrEqual(t, got, 0, "shard number must be non-negative")
			require.Less(t, got, tt.numShards, "shard number must be < numShards")
			require.Equal(t, tt.expectedShard, got,
				"shardForRouting(%q, %d, %d)", tt.routing, tt.routingNumShards, tt.numShards)
		})
	}
}

func TestShardForRouting_AlwaysNonNegative(t *testing.T) {
	// Verify for a range of inputs that the shard number is always valid.
	// Uses routingNumShards == numShards (routingFactor=1) for simplicity.
	for numShards := 1; numShards <= 32; numShards++ {
		for _, routing := range []string{
			"", "a", "ab", "abc", "hello", "routing-key-1",
			"user/123", "tenant:42", "negative-hash-test",
			"The quick brown fox jumps over the lazy dog",
		} {
			shard := shardForRouting(routing, numShards, numShards)
			require.GreaterOrEqual(t, shard, 0,
				"routing=%q numShards=%d", routing, numShards)
			require.Less(t, shard, numShards,
				"routing=%q numShards=%d", routing, numShards)
		}
	}
}

func BenchmarkOpensearchShardHash(b *testing.B) {
	benchmarks := []struct {
		name  string
		input string
	}{
		{"short", "user123"},
		{"medium", "my-tenant/document-id-12345"},
		{"long", "The quick brown fox jumps over the lazy dog and keeps going"},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				opensearchShardHash(bm.input)
			}
		})
	}
}

func BenchmarkShardForRouting(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		shardForRouting("user123", 640, 5)
	}
}

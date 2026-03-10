// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package shardhash_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/shardhash"
)

// Test vectors from OpenSearch server:
// server/src/test/java/org/opensearch/cluster/routing/operation/hash/murmur3/Murmur3HashFunctionTests.java
//
// Java stores these as signed int32 hex literals. In Java, hex int literals
// with bit 31 set are negative (e.g. 0xd7c31989 is a valid int = -675079799).
//
// Source: assertHash(0x..., "...") in Murmur3HashFunctionTests.java
func TestHash_KnownValues(t *testing.T) {
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
			got := shardhash.Hash(tt.input)
			require.Equal(t, tt.expected, got, "Hash(%q)", tt.input)
		})
	}
}

func TestHash_EmptyString(t *testing.T) {
	// Empty string should produce a deterministic hash (not panic).
	// fmix32(0) == 0: the finalization of h1=0 ^ length=0.
	h := shardhash.Hash("")
	require.Equal(t, int32(0), h)
}

func TestHash_SupplementaryCharacter(t *testing.T) {
	t.Parallel()

	t.Run("BMP characters", func(t *testing.T) {
		t.Parallel()
		h := shardhash.Hash("doc123")
		require.NotZero(t, h)
	})

	t.Run("supplementary character triggers surrogate pair", func(t *testing.T) {
		t.Parallel()
		// U+1F600 (😀) is above U+FFFF, exercising the surrogate pair branch
		h := shardhash.Hash("doc\U0001F600")
		require.NotZero(t, h)

		// Must differ from the BMP-only version
		hBMP := shardhash.Hash("doc?")
		require.NotEqual(t, h, hBMP)
	})

	t.Run("long string uses heap buffer", func(t *testing.T) {
		t.Parallel()
		// > 64 code units → heap allocation path (buf = make([]byte, n))
		var longStr strings.Builder
		for range 70 {
			longStr.WriteString("ab")
		}
		h := shardhash.Hash(longStr.String())
		require.NotZero(t, h)
	})
}

func TestForRouting(t *testing.T) {
	// Expected shard values are precomputed from the known hash values:
	//   "hello" -> Hash = -675079799 (0xd7c31989)
	//   "hell"  -> Hash =  1510782915 (0x5a0cb7c3)
	// using Java's Math.floorMod semantics.
	tests := []struct {
		name             string
		routing          string
		routingNumShards int
		numShards        int
		expectedShard    int
	}{
		// routingNumShards == numShards (routingFactor=1): legacy simplified formula
		{"hello/factor1", "hello", 5, 5, 1},
		{"hello/single", "hello", 1, 1, 0}, // single shard -> always 0
		{"hello/factor1/8", "hello", 8, 8, 1},
		{"hell/factor1", "hell", 5, 5, 0},

		// Realistic routingNumShards (from OpenSearch 3.x defaults).
		// 5 shards -> routingNumShards=640, routingFactor=128.
		// shard = floorMod(hash, 640) / 128
		{"hello/5shard_real", "hello", 640, 5, 4},
		{"hell/5shard_real", "hell", 640, 5, 1},

		// 3 shards -> routingNumShards=768, routingFactor=256.
		{"hello/3shard_real", "hello", 768, 3, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shardhash.ForRouting(tt.routing, tt.routingNumShards, tt.numShards)
			require.GreaterOrEqual(t, got, 0, "shard number must be non-negative")
			require.Less(t, got, tt.numShards, "shard number must be < numShards")
			require.Equal(t, tt.expectedShard, got,
				"ForRouting(%q, %d, %d)", tt.routing, tt.routingNumShards, tt.numShards)
		})
	}
}

func TestForRouting_AlwaysNonNegative(t *testing.T) {
	// Verify for a range of inputs that the shard number is always valid.
	// Uses routingNumShards == numShards (routingFactor=1) for simplicity.
	for numShards := 1; numShards <= 32; numShards++ {
		for _, routing := range []string{
			"", "a", "ab", "abc", "hello", "routing-key-1",
			"user/123", "tenant:42", "negative-hash-test",
			"The quick brown fox jumps over the lazy dog",
		} {
			shard := shardhash.ForRouting(routing, numShards, numShards)
			require.GreaterOrEqual(t, shard, 0,
				"routing=%q numShards=%d", routing, numShards)
			require.Less(t, shard, numShards,
				"routing=%q numShards=%d", routing, numShards)
		}
	}
}

func BenchmarkHash(b *testing.B) {
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
				shardhash.Hash(bm.input)
			}
		})
	}
}

func BenchmarkForRouting(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		shardhash.ForRouting("user123", 640, 5)
	}
}

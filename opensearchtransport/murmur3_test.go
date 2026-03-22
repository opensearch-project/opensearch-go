// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport //nolint:testpackage // verifies unexported wrappers delegate correctly

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/shardhash"
)

// TestOpensearchShardHash_DelegatesToShardHash verifies the unexported wrapper
// produces the same result as the exported shardhash.Hash for a representative
// set of inputs.
func TestOpensearchShardHash_DelegatesToShardHash(t *testing.T) {
	for _, input := range []string{
		"", "hell", "hello", "hello w", "hello wo", "hello wor",
		"The quick brown fox jumps over the lazy dog",
		"The quick brown fox jumps over the lazy cog",
		"user/123", "tenant:42",
	} {
		t.Run(input, func(t *testing.T) {
			require.Equal(t, shardhash.Hash(input), opensearchShardHash(input),
				"opensearchShardHash(%q) must match shardhash.Hash", input)
		})
	}
}

// TestShardForRouting_DelegatesToShardHash verifies the unexported wrapper
// produces the same result as the exported shardhash.ForRouting.
func TestShardForRouting_DelegatesToShardHash(t *testing.T) {
	tests := []struct {
		routing          string
		routingNumShards int
		numShards        int
	}{
		{"hello", 5, 5},
		{"hello", 1, 1},
		{"hello", 8, 8},
		{"hell", 5, 5},
		{"hello", 640, 5},
		{"hell", 640, 5},
		{"hello", 768, 3},
	}

	for _, tt := range tests {
		got := shardForRouting(tt.routing, tt.routingNumShards, tt.numShards)
		expected := shardhash.ForRouting(tt.routing, tt.routingNumShards, tt.numShards)
		require.Equal(t, expected, got,
			"shardForRouting(%q, %d, %d) must match shardhash.ForRouting",
			tt.routing, tt.routingNumShards, tt.numShards)
	}
}

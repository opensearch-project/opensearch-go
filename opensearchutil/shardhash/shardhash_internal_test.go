// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package shardhash

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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

func TestFmix32_Zero(t *testing.T) {
	// fmix32(0) == 0: all multiply-then-xor-shift steps produce 0.
	require.Equal(t, int32(0), fmix32(0))
}

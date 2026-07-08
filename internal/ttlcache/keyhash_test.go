// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ttlcache_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/ttlcache"
)

func TestKeyBuilder_Deterministic(t *testing.T) {
	build := func() ttlcache.Key {
		return ttlcache.NewKeyBuilder().
			String("addr").Bytes([]byte{1, 2, 3}).Int(-7).Bool(true).Key()
	}
	first, second := build(), build()
	require.Equal(t, first, second, "same inputs must yield the same key")
}

// TestKeyBuilder_FieldBoundaries covers the length-prefix framing: inputs that
// concatenate to the same bytes under different field boundaries must not
// collide.
func TestKeyBuilder_FieldBoundaries(t *testing.T) {
	tests := []struct {
		name string
		a, b func() ttlcache.Key
	}{
		{
			"string split",
			func() ttlcache.Key { return ttlcache.NewKeyBuilder().String("ab").String("c").Key() },
			func() ttlcache.Key { return ttlcache.NewKeyBuilder().String("a").String("bc").Key() },
		},
		{
			"bool true vs false",
			func() ttlcache.Key { return ttlcache.NewKeyBuilder().Bool(true).Key() },
			func() ttlcache.Key { return ttlcache.NewKeyBuilder().Bool(false).Key() },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NotEqual(t, tt.a(), tt.b(), "distinct field layouts must not collide")
		})
	}
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package slicepool_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/slicepool"
)

func TestAppendReusesElements(t *testing.T) {
	t.Parallel()

	var dst []*int
	*slicepool.Append(&dst) = 1
	*slicepool.Append(&dst) = 2
	first, second := dst[0], dst[1]

	dst = dst[:0]
	reused := slicepool.Append(&dst)
	require.Same(t, first, reused, "the first element is reused")
	require.Equal(t, 1, *reused, "a reused element keeps its old contents")
	require.Same(t, second, slicepool.Append(&dst), "the second element is reused")
	require.NotSame(t, first, slicepool.Append(&dst), "a third element is new")
	require.Len(t, dst, 3)
}

func TestPoolReturnsEmptySlices(t *testing.T) {
	t.Parallel()

	var p slicepool.Pool[int]
	s := p.Get()
	require.Empty(t, *s)
	*slicepool.Append(s) = 1
	p.Put(s)
	require.Empty(t, *s, "Put empties the slice")
	require.Empty(t, *p.Get())
}

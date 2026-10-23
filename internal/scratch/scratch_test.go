// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package scratch_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/scratch"
)

func TestNextReusesElements(t *testing.T) {
	t.Parallel()

	var dst []*int
	*scratch.Next(&dst) = 1
	*scratch.Next(&dst) = 2
	first, second := dst[0], dst[1]

	dst = dst[:0]
	require.Same(t, first, scratch.Next(&dst), "the first element is reused")
	require.Same(t, second, scratch.Next(&dst), "the second element is reused")
	require.NotSame(t, first, scratch.Next(&dst), "a third element is new")
	require.Len(t, dst, 3)
}

func TestPoolReturnsEmptySlices(t *testing.T) {
	t.Parallel()

	var p scratch.Pool[int]
	s := p.Get()
	require.Empty(t, *s)
	*scratch.Next(s) = 1
	p.Put(s)
	require.Empty(t, *s, "Put empties the slice")
	require.Empty(t, *p.Get())
}

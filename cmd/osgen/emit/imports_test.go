// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/emit"
	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

func TestImportSetAdd(t *testing.T) {
	t.Parallel()

	var s emit.ImportSet
	s.Add("net/http")
	s.Add("context")
	s.Add("net/http") // duplicate

	sorted := s.Sorted()
	require.Len(t, sorted, 2)
	require.Equal(t, "context", sorted[0].Path)
	require.Equal(t, "net/http", sorted[1].Path)
}

func TestImportSetAddAlias(t *testing.T) {
	t.Parallel()

	var s emit.ImportSet
	s.AddAlias("github.com/opensearch-project/opensearch-go/v4/internal/path", "ospath")

	sorted := s.Sorted()
	require.Len(t, sorted, 1)
	require.Equal(t, "ospath", sorted[0].Alias)
}

func TestImportSetEmpty(t *testing.T) {
	t.Parallel()

	var s emit.ImportSet
	sorted := s.Sorted()
	require.Nil(t, sorted)
}

func TestImportSetHas(t *testing.T) {
	t.Parallel()

	var s emit.ImportSet
	require.False(t, s.Has("net/http"))
	s.Add("net/http")
	require.True(t, s.Has("net/http"))
}

func TestImportSetGrouped(t *testing.T) {
	t.Parallel()

	var s emit.ImportSet
	s.Add("context")
	s.Add("net/http")
	s.Add("github.com/stretchr/testify/require")
	s.Add("github.com/opensearch-project/opensearch-go/v4")
	s.AddAlias("github.com/opensearch-project/opensearch-go/v4/internal/path", "ospath")

	groups := s.Grouped()

	// Group 0: stdlib
	require.Len(t, groups[0], 2)
	require.Equal(t, "context", groups[0][0].Path)
	require.Equal(t, "net/http", groups[0][1].Path)

	// Group 1: third-party
	require.Len(t, groups[1], 1)
	require.Equal(t, "github.com/stretchr/testify/require", groups[1][0].Path)

	// Group 2: local module
	require.Len(t, groups[2], 2)
	require.Equal(t, "github.com/opensearch-project/opensearch-go/v4", groups[2][0].Path)
	require.Equal(t, "ospath", groups[2][1].Alias)
}

func TestImportGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want int
	}{
		{name: "stdlib", path: "context", want: 0},
		{name: "stdlib nested", path: "net/http", want: 0},
		{name: "third-party", path: "github.com/stretchr/testify/require", want: 1},
		{name: "local root", path: "github.com/opensearch-project/opensearch-go/v4", want: 2},
		{name: "local sub", path: ir.DefaultCoreImportPath, want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, emit.ImportGroup(tt.path))
		})
	}
}

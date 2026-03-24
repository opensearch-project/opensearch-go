// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build !integration

package opensearch_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
)

func TestIndexPath(t *testing.T) {
	t.Run("populated", func(t *testing.T) {
		got, err := opensearch.IndexPath{Index: "my-index"}.Build()
		require.NoError(t, err)
		require.Equal(t, "/my-index", got)
	})
	t.Run("empty returns error", func(t *testing.T) {
		_, err := opensearch.IndexPath{Index: ""}.Build()
		require.ErrorIs(t, err, opensearch.ErrPathRequired)
	})
}

func TestAliasPath(t *testing.T) {
	t.Run("populated", func(t *testing.T) {
		got, err := opensearch.AliasPath{
			Indices: opensearch.Indices{"idx"},
			Alias:   "a1",
		}.Build()
		require.NoError(t, err)
		require.Equal(t, "/idx/_alias/a1", got)
	})
	t.Run("multi-index", func(t *testing.T) {
		got, err := opensearch.AliasPath{
			Indices: opensearch.Indices{"i1", "i2"},
			Alias:   "a1",
		}.Build()
		require.NoError(t, err)
		require.Equal(t, "/i1,i2/_alias/a1", got)
	})
	t.Run("without indices", func(t *testing.T) {
		got, err := opensearch.AliasPath{Indices: nil, Alias: "a1"}.Build()
		require.NoError(t, err)
		require.Equal(t, "/_alias/a1", got)
	})
	t.Run("without alias", func(t *testing.T) {
		got, err := opensearch.AliasPath{Indices: opensearch.Indices{"idx"}, Alias: ""}.Build()
		require.NoError(t, err)
		require.Equal(t, "/idx/_alias", got)
	})
	t.Run("empty", func(t *testing.T) {
		got, err := opensearch.AliasPath{}.Build()
		require.NoError(t, err)
		require.Equal(t, "/_alias", got)
	})
}

func TestDocumentPath(t *testing.T) {
	got, err := opensearch.DocumentPath{Index: "idx", Action: "_doc", DocumentID: "1"}.Build()
	require.NoError(t, err)
	require.Equal(t, "/idx/_doc/1", got)
}

func TestSnapshotPath(t *testing.T) {
	got, err := opensearch.SnapshotPath{Repo: "repo", Snapshot: "snap"}.Build()
	require.NoError(t, err)
	require.Equal(t, "/_snapshot/repo/snap", got)
}

func TestPluginResourcePath(t *testing.T) {
	got, err := opensearch.PluginResourcePath{Plugin: "_security", Resource: "roles", Name: "admin"}.Build()
	require.NoError(t, err)
	require.Equal(t, "/_plugins/_security/api/roles/admin", got)
}

func TestDecommissionPath(t *testing.T) {
	got, err := opensearch.DecommissionPath{Attr: "zone", Value: "us-east-1"}.Build()
	require.NoError(t, err)
	require.Equal(t, "/_cluster/decommission/awareness/zone/us-east-1", got)
}

func TestPrefixActionPath(t *testing.T) {
	t.Run("with prefix", func(t *testing.T) {
		got, err := opensearch.PrefixActionPath{Prefix: "idx", Action: "_bulk"}.Build()
		require.NoError(t, err)
		require.Equal(t, "/idx/_bulk", got)
	})
	t.Run("without prefix", func(t *testing.T) {
		got, err := opensearch.PrefixActionPath{Prefix: "", Action: "_bulk"}.Build()
		require.NoError(t, err)
		require.Equal(t, "/_bulk", got)
	})
}

func TestIndicesJoin(t *testing.T) {
	require.Equal(t, "", opensearch.Indices(nil).Join())
	require.Equal(t, "a", opensearch.Indices{"a"}.Join())
	require.Equal(t, "a,b,c", opensearch.Indices{"a", "b", "c"}.Join())
}

func TestMustBuild(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		got := opensearch.MustBuild(opensearch.IndexPath{Index: "idx"}.Build())
		require.Equal(t, "/idx", got)
	})
	t.Run("panics on error", func(t *testing.T) {
		require.Panics(t, func() {
			opensearch.MustBuild(opensearch.IndexPath{Index: ""}.Build())
		})
	})
}

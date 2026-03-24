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

type pathBuilder interface {
	Build() (string, error)
}

var (
	_ pathBuilder = opensearch.IndexPath{}
	_ pathBuilder = opensearch.IndexActionPath{}
	_ pathBuilder = opensearch.DocumentPath{}
	_ pathBuilder = opensearch.IndexTargetPath{}
	_ pathBuilder = opensearch.IndicesActionPath{}
	_ pathBuilder = opensearch.IndicesBlockPath{}
	_ pathBuilder = opensearch.AliasPath{}
	_ pathBuilder = opensearch.ResourcePath{}
	_ pathBuilder = opensearch.ResourceActionPath{}
	_ pathBuilder = opensearch.SnapshotPath{}
	_ pathBuilder = opensearch.SnapshotActionPath{}
	_ pathBuilder = opensearch.SnapshotClonePath{}
	_ pathBuilder = opensearch.PluginResourcePath{}
	_ pathBuilder = opensearch.PluginIndexPath{}
	_ pathBuilder = opensearch.PluginPolicyPath{}
	_ pathBuilder = opensearch.DecommissionPath{}
	_ pathBuilder = opensearch.PrefixActionPath{}
	_ pathBuilder = opensearch.PrefixActionSuffixPath{}
	_ pathBuilder = opensearch.ActionSuffixPath{}
	_ pathBuilder = opensearch.PrefixSuffixActionPath{}
	_ pathBuilder = opensearch.RolloverPath{}
	_ pathBuilder = opensearch.TermvectorsPath{}
	_ pathBuilder = opensearch.NodesPath{}
	_ pathBuilder = opensearch.ClusterStatePath{}
	_ pathBuilder = opensearch.ClusterStatsPath{}
)

func TestPathBuilders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		build   func() (string, error)
		want    string
		wantErr bool
	}{
		// IndexPath
		{name: "IndexPath/populated", build: func() (string, error) {
			return opensearch.IndexPath{Index: "my-index"}.Build()
		}, want: "/my-index"},
		{name: "IndexPath/empty", build: func() (string, error) {
			return opensearch.IndexPath{}.Build()
		}, wantErr: true},

		// IndexActionPath
		{name: "IndexActionPath/populated", build: func() (string, error) {
			return opensearch.IndexActionPath{Index: "idx", Action: "_open"}.Build()
		}, want: "/idx/_open"},
		{name: "IndexActionPath/empty_index", build: func() (string, error) {
			return opensearch.IndexActionPath{Action: "_open"}.Build()
		}, wantErr: true},
		{name: "IndexActionPath/empty_action", build: func() (string, error) {
			return opensearch.IndexActionPath{Index: "idx"}.Build()
		}, wantErr: true},

		// DocumentPath
		{name: "DocumentPath/populated", build: func() (string, error) {
			return opensearch.DocumentPath{Index: "idx", Action: "_doc", DocumentID: "1"}.Build()
		}, want: "/idx/_doc/1"},
		{name: "DocumentPath/empty_index", build: func() (string, error) {
			return opensearch.DocumentPath{Action: "_doc", DocumentID: "1"}.Build()
		}, wantErr: true},
		{name: "DocumentPath/empty_action", build: func() (string, error) {
			return opensearch.DocumentPath{Index: "idx", DocumentID: "1"}.Build()
		}, wantErr: true},
		{name: "DocumentPath/empty_docid", build: func() (string, error) {
			return opensearch.DocumentPath{Index: "idx", Action: "_doc"}.Build()
		}, wantErr: true},

		// IndexTargetPath
		{name: "IndexTargetPath/populated", build: func() (string, error) {
			return opensearch.IndexTargetPath{Index: "src", Action: "_clone", Target: "dst"}.Build()
		}, want: "/src/_clone/dst"},
		{name: "IndexTargetPath/empty", build: func() (string, error) {
			return opensearch.IndexTargetPath{}.Build()
		}, wantErr: true},

		// IndicesActionPath
		{name: "IndicesActionPath/populated", build: func() (string, error) {
			return opensearch.IndicesActionPath{Indices: opensearch.Indices{"i1", "i2"}, Action: "_search"}.Build()
		}, want: "/i1,i2/_search"},
		{name: "IndicesActionPath/empty_indices", build: func() (string, error) {
			return opensearch.IndicesActionPath{Action: "_search"}.Build()
		}, wantErr: true},
		{name: "IndicesActionPath/empty_action", build: func() (string, error) {
			return opensearch.IndicesActionPath{Indices: opensearch.Indices{"i1"}}.Build()
		}, wantErr: true},

		// IndicesBlockPath
		{name: "IndicesBlockPath/populated", build: func() (string, error) {
			return opensearch.IndicesBlockPath{Indices: opensearch.Indices{"idx"}, Block: "write"}.Build()
		}, want: "/idx/_block/write"},
		{name: "IndicesBlockPath/empty_indices", build: func() (string, error) {
			return opensearch.IndicesBlockPath{Block: "write"}.Build()
		}, wantErr: true},
		{name: "IndicesBlockPath/empty_block", build: func() (string, error) {
			return opensearch.IndicesBlockPath{Indices: opensearch.Indices{"idx"}}.Build()
		}, wantErr: true},

		// AliasPath (all optional)
		{name: "AliasPath/full", build: func() (string, error) {
			return opensearch.AliasPath{Indices: opensearch.Indices{"idx"}, Alias: "a1"}.Build()
		}, want: "/idx/_alias/a1"},
		{name: "AliasPath/multi_index", build: func() (string, error) {
			return opensearch.AliasPath{Indices: opensearch.Indices{"i1", "i2"}, Alias: "a1"}.Build()
		}, want: "/i1,i2/_alias/a1"},
		{name: "AliasPath/no_indices", build: func() (string, error) {
			return opensearch.AliasPath{Alias: "a1"}.Build()
		}, want: "/_alias/a1"},
		{name: "AliasPath/no_alias", build: func() (string, error) {
			return opensearch.AliasPath{Indices: opensearch.Indices{"idx"}}.Build()
		}, want: "/idx/_alias"},
		{name: "AliasPath/empty", build: func() (string, error) {
			return opensearch.AliasPath{}.Build()
		}, want: "/_alias"},

		// ResourcePath
		{name: "ResourcePath/populated", build: func() (string, error) {
			return opensearch.ResourcePath{Prefix: "_template", Name: "tmpl1"}.Build()
		}, want: "/_template/tmpl1"},
		{name: "ResourcePath/empty_prefix", build: func() (string, error) {
			return opensearch.ResourcePath{Name: "tmpl1"}.Build()
		}, wantErr: true},
		{name: "ResourcePath/empty_name", build: func() (string, error) {
			return opensearch.ResourcePath{Prefix: "_template"}.Build()
		}, wantErr: true},

		// ResourceActionPath
		{name: "ResourceActionPath/populated", build: func() (string, error) {
			return opensearch.ResourceActionPath{Prefix: "_snapshot", Name: "repo", Action: "_verify"}.Build()
		}, want: "/_snapshot/repo/_verify"},
		{name: "ResourceActionPath/empty", build: func() (string, error) {
			return opensearch.ResourceActionPath{}.Build()
		}, wantErr: true},

		// SnapshotPath
		{name: "SnapshotPath/populated", build: func() (string, error) {
			return opensearch.SnapshotPath{Repo: "repo", Snapshot: "snap"}.Build()
		}, want: "/_snapshot/repo/snap"},
		{name: "SnapshotPath/empty_repo", build: func() (string, error) {
			return opensearch.SnapshotPath{Snapshot: "snap"}.Build()
		}, wantErr: true},
		{name: "SnapshotPath/empty_snapshot", build: func() (string, error) {
			return opensearch.SnapshotPath{Repo: "repo"}.Build()
		}, wantErr: true},

		// SnapshotActionPath
		{name: "SnapshotActionPath/populated", build: func() (string, error) {
			return opensearch.SnapshotActionPath{Repo: "repo", Snapshot: "snap", Action: "_restore"}.Build()
		}, want: "/_snapshot/repo/snap/_restore"},
		{name: "SnapshotActionPath/empty", build: func() (string, error) {
			return opensearch.SnapshotActionPath{}.Build()
		}, wantErr: true},

		// SnapshotClonePath
		{name: "SnapshotClonePath/populated", build: func() (string, error) {
			return opensearch.SnapshotClonePath{Repo: "repo", Snapshot: "snap", TargetSnapshot: "clone"}.Build()
		}, want: "/_snapshot/repo/snap/_clone/clone"},
		{name: "SnapshotClonePath/empty", build: func() (string, error) {
			return opensearch.SnapshotClonePath{}.Build()
		}, wantErr: true},

		// PluginResourcePath
		{name: "PluginResourcePath/populated", build: func() (string, error) {
			return opensearch.PluginResourcePath{Plugin: "_security", Resource: "roles", Name: "admin"}.Build()
		}, want: "/_plugins/_security/api/roles/admin"},
		{name: "PluginResourcePath/empty", build: func() (string, error) {
			return opensearch.PluginResourcePath{}.Build()
		}, wantErr: true},

		// PluginIndexPath
		{name: "PluginIndexPath/with_indices", build: func() (string, error) {
			return opensearch.PluginIndexPath{Plugin: "_ism", Action: "_explain", Indices: opensearch.Indices{"idx"}}.Build()
		}, want: "/_plugins/_ism/_explain/idx"},
		{name: "PluginIndexPath/without_indices", build: func() (string, error) {
			return opensearch.PluginIndexPath{Plugin: "_ism", Action: "_explain"}.Build()
		}, want: "/_plugins/_ism/_explain"},
		{name: "PluginIndexPath/empty", build: func() (string, error) {
			return opensearch.PluginIndexPath{}.Build()
		}, wantErr: true},

		// PluginPolicyPath
		{name: "PluginPolicyPath/populated", build: func() (string, error) {
			return opensearch.PluginPolicyPath{Plugin: "_ism", Policy: "rollover"}.Build()
		}, want: "/_plugins/_ism/policies/rollover"},
		{name: "PluginPolicyPath/empty", build: func() (string, error) {
			return opensearch.PluginPolicyPath{}.Build()
		}, wantErr: true},

		// DecommissionPath
		{name: "DecommissionPath/populated", build: func() (string, error) {
			return opensearch.DecommissionPath{Attr: "zone", Value: "us-east-1"}.Build()
		}, want: "/_cluster/decommission/awareness/zone/us-east-1"},
		{name: "DecommissionPath/empty_attr", build: func() (string, error) {
			return opensearch.DecommissionPath{Value: "us-east-1"}.Build()
		}, wantErr: true},
		{name: "DecommissionPath/empty_value", build: func() (string, error) {
			return opensearch.DecommissionPath{Attr: "zone"}.Build()
		}, wantErr: true},

		// PrefixActionPath
		{name: "PrefixActionPath/with_prefix", build: func() (string, error) {
			return opensearch.PrefixActionPath{Prefix: "idx", Action: "_bulk"}.Build()
		}, want: "/idx/_bulk"},
		{name: "PrefixActionPath/without_prefix", build: func() (string, error) {
			return opensearch.PrefixActionPath{Action: "_bulk"}.Build()
		}, want: "/_bulk"},
		{name: "PrefixActionPath/empty_action", build: func() (string, error) {
			return opensearch.PrefixActionPath{Prefix: "idx"}.Build()
		}, wantErr: true},

		// PrefixActionSuffixPath
		{name: "PrefixActionSuffixPath/full", build: func() (string, error) {
			return opensearch.PrefixActionSuffixPath{Prefix: "idx", Action: "_settings", Suffix: "index.number_of_replicas"}.Build()
		}, want: "/idx/_settings/index.number_of_replicas"},
		{name: "PrefixActionSuffixPath/no_prefix_no_suffix", build: func() (string, error) {
			return opensearch.PrefixActionSuffixPath{Action: "_settings"}.Build()
		}, want: "/_settings"},
		{name: "PrefixActionSuffixPath/empty_action", build: func() (string, error) {
			return opensearch.PrefixActionSuffixPath{Prefix: "idx"}.Build()
		}, wantErr: true},

		// ActionSuffixPath
		{name: "ActionSuffixPath/with_suffix", build: func() (string, error) {
			return opensearch.ActionSuffixPath{Action: "_plugins/_security/api/roles", Suffix: "admin"}.Build()
		}, want: "/_plugins/_security/api/roles/admin"},
		{name: "ActionSuffixPath/without_suffix", build: func() (string, error) {
			return opensearch.ActionSuffixPath{Action: "_plugins/_security/api/roles"}.Build()
		}, want: "/_plugins/_security/api/roles"},
		{name: "ActionSuffixPath/empty_action", build: func() (string, error) {
			return opensearch.ActionSuffixPath{Suffix: "admin"}.Build()
		}, wantErr: true},

		// PrefixSuffixActionPath
		{name: "PrefixSuffixActionPath/full", build: func() (string, error) {
			return opensearch.PrefixSuffixActionPath{Prefix: "_tasks", Suffix: "abc123", Action: "_cancel"}.Build()
		}, want: "/_tasks/abc123/_cancel"},
		{name: "PrefixSuffixActionPath/no_suffix", build: func() (string, error) {
			return opensearch.PrefixSuffixActionPath{Prefix: "_ingest/pipeline", Action: "_simulate"}.Build()
		}, want: "/_ingest/pipeline/_simulate"},
		{name: "PrefixSuffixActionPath/empty", build: func() (string, error) {
			return opensearch.PrefixSuffixActionPath{}.Build()
		}, wantErr: true},

		// RolloverPath
		{name: "RolloverPath/with_index", build: func() (string, error) {
			return opensearch.RolloverPath{Alias: "logs", Index: "logs-000002"}.Build()
		}, want: "/logs/_rollover/logs-000002"},
		{name: "RolloverPath/without_index", build: func() (string, error) {
			return opensearch.RolloverPath{Alias: "logs"}.Build()
		}, want: "/logs/_rollover"},
		{name: "RolloverPath/empty_alias", build: func() (string, error) {
			return opensearch.RolloverPath{Index: "logs-000002"}.Build()
		}, wantErr: true},

		// TermvectorsPath (all optional)
		{name: "TermvectorsPath/full", build: func() (string, error) {
			return opensearch.TermvectorsPath{Index: "idx", DocumentID: "1"}.Build()
		}, want: "/idx/_termvectors/1"},
		{name: "TermvectorsPath/no_index", build: func() (string, error) {
			return opensearch.TermvectorsPath{DocumentID: "1"}.Build()
		}, want: "/_termvectors/1"},
		{name: "TermvectorsPath/no_docid", build: func() (string, error) {
			return opensearch.TermvectorsPath{Index: "idx"}.Build()
		}, want: "/idx/_termvectors"},
		{name: "TermvectorsPath/empty", build: func() (string, error) {
			return opensearch.TermvectorsPath{}.Build()
		}, want: "/_termvectors"},

		// NodesPath
		{name: "NodesPath/full", build: func() (string, error) {
			return opensearch.NodesPath{NodeID: "node1", Action: "_stats", Metric: "jvm", IndexMetric: "fielddata"}.Build()
		}, want: "/_nodes/node1/_stats/jvm/fielddata"},
		{name: "NodesPath/action_only", build: func() (string, error) {
			return opensearch.NodesPath{Action: "_info"}.Build()
		}, want: "/_nodes/_info"},
		{name: "NodesPath/empty_action", build: func() (string, error) {
			return opensearch.NodesPath{NodeID: "node1"}.Build()
		}, wantErr: true},

		// ClusterStatePath (all optional)
		{name: "ClusterStatePath/full", build: func() (string, error) {
			return opensearch.ClusterStatePath{Metrics: "metadata", Indices: opensearch.Indices{"idx"}}.Build()
		}, want: "/_cluster/state/metadata/idx"},
		{name: "ClusterStatePath/no_metrics", build: func() (string, error) {
			return opensearch.ClusterStatePath{Indices: opensearch.Indices{"idx"}}.Build()
		}, want: "/_cluster/state/idx"},
		{name: "ClusterStatePath/empty", build: func() (string, error) {
			return opensearch.ClusterStatePath{}.Build()
		}, want: "/_cluster/state"},

		// ClusterStatsPath (all optional)
		{name: "ClusterStatsPath/with_filter", build: func() (string, error) {
			return opensearch.ClusterStatsPath{NodeFilter: "data:true"}.Build()
		}, want: "/_cluster/stats/nodes/data:true"},
		{name: "ClusterStatsPath/empty", build: func() (string, error) {
			return opensearch.ClusterStatsPath{}.Build()
		}, want: "/_cluster/stats"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.build()
			if tt.wantErr {
				require.ErrorIs(t, err, opensearch.ErrPathRequired)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestToIndices(t *testing.T) {
	t.Parallel()
	got := opensearch.ToIndices([]string{"a", "b", "c"})
	require.Equal(t, opensearch.Indices{"a", "b", "c"}, got)
}

func TestIndicesJoin(t *testing.T) {
	t.Parallel()
	require.Equal(t, "", opensearch.Indices(nil).Join())
	require.Equal(t, "a", opensearch.Indices{"a"}.Join())
	require.Equal(t, "a,b,c", opensearch.Indices{"a", "b", "c"}.Join())
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTypeRegistryRegister(t *testing.T) {
	t.Parallel()

	r := newTypeRegistry(opensearchAPIPkgName)

	t1 := &goType{Name: "ShardStatistics", SchemaRef: "_common___ShardStatistics", IsShared: true}
	got, ok := r.register(t1)
	require.True(t, ok)
	require.Equal(t, t1, got)

	// Duplicate schema ref returns existing.
	t2 := &goType{Name: "ShardStatistics", SchemaRef: "_common___ShardStatistics"}
	got, ok = r.register(t2)
	require.False(t, ok)
	require.Equal(t, t1, got)

	// Name collision with different schema ref.
	t3 := &goType{Name: "ShardStatistics", SchemaRef: "other___ShardStatistics"}
	got, ok = r.register(t3)
	require.False(t, ok)
	require.Nil(t, got)
}

func TestTypeRegistryLookup(t *testing.T) {
	t.Parallel()

	r := newTypeRegistry(opensearchAPIPkgName)
	t1 := &goType{Name: "ErrorCause", SchemaRef: "_common___ErrorCause", IsShared: true}
	r.register(t1)

	got, ok := r.lookup("_common___ErrorCause")
	require.True(t, ok)
	require.Equal(t, t1, got)

	_, ok = r.lookup("nonexistent")
	require.False(t, ok)

	got, ok = r.lookupByName("ErrorCause")
	require.True(t, ok)
	require.Equal(t, t1, got)
}

func TestTypeRegistryAll(t *testing.T) {
	t.Parallel()

	r := newTypeRegistry(opensearchAPIPkgName)
	t1 := &goType{Name: "A", SchemaRef: "ref_a"}
	t2 := &goType{Name: "B", SchemaRef: "ref_b"}
	t3 := &goType{Name: "C", SchemaRef: "ref_c"}
	r.register(t1)
	r.register(t2)
	r.register(t3)

	all := r.all()
	require.Len(t, all, 3)
	require.Equal(t, "A", all[0].Name)
	require.Equal(t, "B", all[1].Name)
	require.Equal(t, "C", all[2].Name)
}

func TestTypeRegistryShared(t *testing.T) {
	t.Parallel()

	r := newTypeRegistry(opensearchAPIPkgName)
	r.register(&goType{Name: "Zebra", SchemaRef: "ref_z", IsShared: true})
	r.register(&goType{Name: "Alpha", SchemaRef: "ref_a", IsShared: true})
	r.register(&goType{Name: "OpSpecific", SchemaRef: "cluster.health___Something"})

	shared := r.shared()
	require.Len(t, shared, 2)
	require.Equal(t, "Alpha", shared[0].Name)
	require.Equal(t, "Zebra", shared[1].Name)
}

func TestTypeRegistryForOperation(t *testing.T) {
	t.Parallel()

	r := newTypeRegistry(opensearchAPIPkgName)
	r.register(&goType{Name: "ShardStatistics", SchemaRef: "_common___ShardStatistics", IsShared: true})
	r.register(&goType{Name: "ClusterHealthIndexStats", SchemaRef: "cluster.health___IndexHealthStats"})
	r.register(&goType{Name: "ClusterHealthResp", SchemaRef: "cluster.health___HealthResponseBody"})
	r.register(&goType{Name: "IndicesRefreshResp", SchemaRef: "indices.refresh___RefreshResponseBody"})

	opTypes := r.forOperation("cluster.health")
	require.Len(t, opTypes, 2)
	require.Equal(t, "ClusterHealthIndexStats", opTypes[0].Name)
	require.Equal(t, "ClusterHealthResp", opTypes[1].Name)
}

func TestSchemaGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  string
		want string
	}{
		{name: "dotted group", ref: "cluster.health___IndexHealthStats", want: "cluster.health"},
		{name: "common", ref: "_common___ShardStatistics", want: "_common"},
		{name: "no separator", ref: "something", want: ""},
		{name: "group._common", ref: "nodes._common___NodesResponseBase", want: "nodes._common"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, schemaGroup(tt.ref))
		})
	}
}

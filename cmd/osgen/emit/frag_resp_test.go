// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

func TestRespFragment_Body(t *testing.T) {
	t.Parallel()

	op := &ir.Operation{
		Group:       "cluster.health",
		TypePrefix:  "ClusterHealth",
		Description: "Returns cluster health.",
		Response: &ir.Type{
			Name: "ClusterHealthResp",
			Fields: []ir.Field{
				{GoName: "ClusterName", JSONName: "cluster_name", GoType: "string"},
				{GoName: "Status", JSONName: "status", GoType: "string"},
				{GoName: "NumberOfNodes", JSONName: "number_of_nodes", GoType: "int", Comment: "Total nodes in the cluster."},
			},
		},
	}

	frag := &RespFragment{Op: op}

	body, err := frag.Body()
	require.NoError(t, err)

	require.Contains(t, body, "type ClusterHealthResp struct")
	require.Contains(t, body, `ClusterName string`)
	require.Contains(t, body, `json:"cluster_name"`)
	require.Contains(t, body, "response *opensearch.Response")
	require.Contains(t, body, "func (r ClusterHealthResp) Inspect() Inspect")
}

func TestRespFragment_NoBody(t *testing.T) {
	t.Parallel()

	op := &ir.Operation{
		TypePrefix: "IndicesExists",
		IsNoBody:   true,
	}

	frag := &RespFragment{Op: op}

	body, err := frag.Body()
	require.NoError(t, err)
	require.Empty(t, body)

	imps := frag.Imports()
	require.Empty(t, imps)
}

func TestDispatchFragment_Body(t *testing.T) {
	t.Parallel()

	op := &ir.Operation{
		TypePrefix:   "ClusterHealth",
		IsPointerReq: true,
		DispatchRoutes: []ir.DispatchRoute{
			{ReceiverType: "clusterClient", MethodName: "Health", TopLevel: false},
		},
	}

	frag := &DispatchFragment{Op: op}

	body, err := frag.Body()
	require.NoError(t, err)

	require.Contains(t, body, "func (c clusterClient) Health(ctx context.Context, req *ClusterHealthReq)")
	require.Contains(t, body, "if req == nil")
	require.Contains(t, body, "c.apiClient")
}

func TestDispatchFragment_TopLevel(t *testing.T) {
	t.Parallel()

	op := &ir.Operation{
		TypePrefix:   "Info",
		IsPointerReq: true,
		DispatchRoutes: []ir.DispatchRoute{
			{ReceiverType: "Client", MethodName: "Info", TopLevel: true},
		},
	}

	frag := &DispatchFragment{Op: op}

	body, err := frag.Body()
	require.NoError(t, err)

	require.Contains(t, body, "&c")
}

func TestDispatchFragment_NoBody(t *testing.T) {
	t.Parallel()

	op := &ir.Operation{
		TypePrefix:   "IndicesExists",
		IsNoBody:     true,
		IsPointerReq: true,
		DispatchRoutes: []ir.DispatchRoute{
			{ReceiverType: "indicesClient", MethodName: "Exists", TopLevel: false},
		},
	}

	frag := &DispatchFragment{Op: op}

	body, err := frag.Body()
	require.NoError(t, err)

	require.Contains(t, body, "*opensearch.Response")
	require.Contains(t, body, "noBody")
}

func TestSiblingTypesFragment_Body(t *testing.T) {
	t.Parallel()

	op := &ir.Operation{Group: "cluster.health"}
	types := []*ir.Type{
		{
			Name: "IndexHealthStats",
			Fields: []ir.Field{
				{GoName: "Status", JSONName: "status", GoType: "string"},
				{GoName: "NumberOfShards", JSONName: "number_of_shards", GoType: "int"},
			},
		},
	}

	frag := &SiblingTypesFragment{Op: op, Types: types}

	body, err := frag.Body()
	require.NoError(t, err)

	require.Contains(t, body, "type IndexHealthStats struct")
	require.Contains(t, body, "cluster.health operation")
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/emit"
	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
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

	frag := &emit.RespFragment{Op: op}

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

	frag := &emit.RespFragment{Op: op}

	body, err := frag.Body()
	require.NoError(t, err)
	require.Empty(t, body)

	imps := frag.Imports()
	require.Empty(t, imps)
}

func TestDispatchFragment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		op             *ir.Operation
		wantContains   []string
		wantNotContain []string
	}{
		{
			name: "pointer req, sub-client",
			op: &ir.Operation{
				TypePrefix:   "ClusterHealth",
				IsPointerReq: true,
				DispatchRoutes: []ir.DispatchRoute{
					{ReceiverType: "ClusterClient", MethodName: "Health", TopLevel: false},
				},
			},
			wantContains: []string{
				"func (c ClusterClient) Health(ctx context.Context, req *ClusterHealthReq)",
				"if req == nil",
				"c.apiClient",
			},
		},
		{
			name: "top-level client",
			op: &ir.Operation{
				TypePrefix:   "Info",
				IsPointerReq: true,
				DispatchRoutes: []ir.DispatchRoute{
					{ReceiverType: "Client", MethodName: "Info", TopLevel: true},
				},
			},
			wantContains: []string{"&c"},
		},
		{
			name: "no-body op",
			op: &ir.Operation{
				TypePrefix:   "IndicesExists",
				IsNoBody:     true,
				IsPointerReq: true,
				DispatchRoutes: []ir.DispatchRoute{
					{ReceiverType: "IndicesClient", MethodName: "Exists", TopLevel: false},
				},
			},
			wantContains: []string{"*opensearch.Response", "noBody"},
		},
		{
			// Dual-method (GET, POST) with body: dispatch must emit a runtime
			// switch so non-nil bodies issue POST. Without this, search and
			// peers silently issue GET-with-body, which most proxies drop per
			// RFC 7231 4.3.1.
			name: "dual-method body: switches GET to POST when body present",
			op: &ir.Operation{
				TypePrefix:   "Search",
				IsPointerReq: true,
				HasBody:      true,
				HasTypedBody: true,
				HTTPMethods:  []string{http.MethodGet, http.MethodPost},
				DispatchRoutes: []ir.DispatchRoute{
					{ReceiverType: "Client", MethodName: "Search", TopLevel: true},
				},
			},
			wantContains: []string{
				"method := http.MethodGet",
				"if req.Body != nil || req.BodyReader != nil",
				"method = http.MethodPost",
			},
			wantNotContain: []string{
				// The do() call must use the computed method, not a constant.
				"http.MethodGet,\n\t\treq, &data",
			},
		},
		{
			// Value-receiver path (IsPointerReq: false) had no unit coverage
			// before; commit 8a48b432 fixed a reqXxxReq concatenation bug here.
			name: "value receiver: no nil-check, no type concat glitch",
			op: &ir.Operation{
				TypePrefix:   "Bulk",
				IsPointerReq: false,
				HasBody:      true,
				HasTypedBody: true,
				HTTPMethods:  []string{http.MethodPost},
				DispatchRoutes: []ir.DispatchRoute{
					{ReceiverType: "Client", MethodName: "Bulk", TopLevel: true},
				},
			},
			wantContains:   []string{"func (c Client) Bulk(ctx context.Context, req BulkReq)"},
			wantNotContain: []string{"if req == nil", "reqBulkReq"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body, err := (&emit.DispatchFragment{Op: tt.op}).Body()
			require.NoError(t, err)

			for _, sub := range tt.wantContains {
				require.Contains(t, body, sub)
			}
			for _, sub := range tt.wantNotContain {
				require.NotContains(t, body, sub)
			}
		})
	}
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

	frag := &emit.SiblingTypesFragment{Op: op, Types: types}

	body, err := frag.Body()
	require.NoError(t, err)

	require.Contains(t, body, "type IndexHealthStats struct")
	require.Contains(t, body, "cluster.health operation")
}

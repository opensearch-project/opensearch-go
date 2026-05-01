// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

func TestReqFragment_SimpleOp(t *testing.T) {
	t.Parallel()

	op := &ir.Operation{
		Group:       "cluster.health",
		TypePrefix:  "ClusterHealth",
		Description: "Returns cluster health.",
		HTTPMethods: []string{"GET"},
		PrimaryPath: "/_cluster/health",
		PathBuilder: ir.PathBuilder{StructName: "ClusterHealthPath"},
	}

	frag := &ReqFragment{Op: op}

	body, err := frag.Body()
	require.NoError(t, err)

	require.Contains(t, body, "ClusterHealthReq")
	require.Contains(t, body, "GetRequest(method string)")
	require.Contains(t, body, "ospath.ClusterHealthPath")
	require.Contains(t, body, "path, err :=")
	require.NotContains(t, body, "io.Reader")
}

func TestReqFragment_WithBodyAndPathFields(t *testing.T) {
	t.Parallel()

	op := &ir.Operation{
		Group:       "index",
		TypePrefix:  "Index",
		Description: "Indexes a document.",
		HTTPMethods: []string{"PUT", "POST"},
		PrimaryPath: "/{index}/_doc/{id}",
		HasBody:     true,
		PathFields: []ir.PathField{
			{GoName: "Index", IsList: false, Required: true},
			{GoName: "ID", IsList: false, Required: false},
		},
		PathBuilder: ir.PathBuilder{
			StructName: "IndexPath",
		},
	}

	frag := &ReqFragment{Op: op}

	body, err := frag.Body()
	require.NoError(t, err)

	require.Contains(t, body, "Body   io.Reader")
	require.Contains(t, body, "Index string")
	require.Contains(t, body, "ID string")
	require.Contains(t, body, "path, err :=")
	require.Contains(t, body, "r.Body,")
	require.NotContains(t, body, "r.Body != nil")
}

func TestReqFragment_Imports(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hasBody bool
		wantIO  bool
	}{
		{name: "no body", hasBody: false, wantIO: false},
		{name: "with body", hasBody: true, wantIO: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			op := &ir.Operation{HasBody: tt.hasBody}
			frag := &ReqFragment{Op: op}
			imps := frag.Imports()

			hasIO := false
			for _, imp := range imps {
				if imp.Path == "io" {
					hasIO = true
				}
			}
			require.Equal(t, tt.wantIO, hasIO, "io import mismatch")
		})
	}
}

func TestParamsFragment_SimpleOp(t *testing.T) {
	t.Parallel()

	op := &ir.Operation{
		TypePrefix: "ClusterHealth",
		QueryParams: []ir.QueryParam{
			{GoName: "Timeout", WireName: "timeout", GoType: "time.Duration", Kind: ir.ParamDuration, Description: "Request timeout."},
			{GoName: "Local", WireName: "local", GoType: "bool", Kind: ir.ParamBool},
			{GoName: "Level", WireName: "level", GoType: "string", Kind: ir.ParamString, Default: "cluster"},
		},
	}

	frag := &ParamsFragment{Op: op}

	body, err := frag.Body()
	require.NoError(t, err)

	require.Contains(t, body, "ClusterHealthParams")
	require.Contains(t, body, `formatDuration(r.Timeout)`)
	require.Contains(t, body, `params["local"] = "true"`)
	require.Contains(t, body, `params["level"] = r.Level`)
	require.Contains(t, body, "// Default: cluster.")
	require.Contains(t, body, "make(map[string]string, 3)")
}

func TestParamsFragment_Imports(t *testing.T) {
	t.Parallel()

	op := &ir.Operation{
		QueryParams: []ir.QueryParam{
			{GoName: "Timeout", WireName: "timeout", GoType: "time.Duration", Kind: ir.ParamDuration},
			{GoName: "Size", WireName: "size", GoType: "int", Kind: ir.ParamInt},
			{GoName: "Index", WireName: "index", GoType: "[]string", Kind: ir.ParamList},
		},
	}

	frag := &ParamsFragment{Op: op}
	imps := frag.Imports()

	paths := make(map[string]bool)
	for _, imp := range imps {
		paths[imp.Path] = true
	}

	require.True(t, paths["time"], "missing time import for Duration param")
	require.True(t, paths["strconv"], "missing strconv import for Int param")
	require.True(t, paths["strings"], "missing strings import for List param / FilterPath")
}

func TestFileAssembly_ReqAndParams(t *testing.T) {
	t.Parallel()

	op := &ir.Operation{
		Group:       "cluster.health",
		TypePrefix:  "ClusterHealth",
		Description: "Returns cluster health.",
		HTTPMethods: []string{"GET"},
		PrimaryPath: "/_cluster/health",
		PathBuilder: ir.PathBuilder{StructName: "ClusterHealthPath"},
		QueryParams: []ir.QueryParam{
			{GoName: "Timeout", WireName: "timeout", GoType: "time.Duration", Kind: ir.ParamDuration},
		},
	}

	f := &File{
		FilePath:  "/tmp/test/cluster-health_gen.go",
		Package:   "osapi",
		Fragments: []Fragment{&ReqFragment{Op: op}, &ParamsFragment{Op: op}},
	}

	src, err := f.Render()
	require.NoError(t, err)

	output := string(src)
	require.Contains(t, output, "package osapi")

	netIdx := strings.Index(output, `"net/http"`)
	opensearchIdx := strings.Index(output, `"github.com/opensearch-project/opensearch-go/v4"`)
	require.Greater(t, netIdx, 0, "missing net/http import")
	require.Greater(t, opensearchIdx, 0, "missing opensearch import")
	require.Less(t, netIdx, opensearchIdx, "stdlib imports should precede local module imports")

	require.Contains(t, output, "type ClusterHealthReq struct")
	require.Contains(t, output, "type ClusterHealthParams struct")
}

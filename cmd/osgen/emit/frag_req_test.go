// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/emit"
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

	frag := &emit.ReqFragment{Op: op}

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

	frag := &emit.ReqFragment{Op: op}

	body, err := frag.Body()
	require.NoError(t, err)

	require.Contains(t, body, "Body   io.Reader")
	require.Contains(t, body, "Index string")
	require.Contains(t, body, "ID string")
	require.Contains(t, body, "path, err :=")
	require.Contains(t, body, "r.Body,")
	require.NotContains(t, body, "r.Body != nil")
}

func TestReqFragment_WithTypedBody(t *testing.T) {
	t.Parallel()

	op := &ir.Operation{
		Group:        "ml.register_model",
		TypePrefix:   "MlRegisterModel",
		Description:  "Registers a model.",
		HTTPMethods:  []string{"POST"},
		PrimaryPath:  "/_plugins/_ml/models/_register",
		HasBody:      true,
		HasTypedBody: true,
		RequestBody: &ir.Type{
			Name: "MlRegisterModelBody",
			Kind: ir.TypeStruct,
			Fields: []ir.Field{
				{GoName: "Name", JSONName: "name", GoType: "string"},
				{GoName: "Version", JSONName: "version", GoType: "string"},
			},
		},
		PathBuilder: ir.PathBuilder{StructName: "MlRegisterModelPath"},
	}

	frag := &emit.ReqFragment{Op: op}

	body, err := frag.Body()
	require.NoError(t, err)

	require.Contains(t, body, "Body *MlRegisterModelBody")
	require.Contains(t, body, "BodyReader io.Reader")
	require.Contains(t, body, "json.Marshal(r.Body)")
	require.Contains(t, body, "r.BodyReader != nil")
	require.NotContains(t, body, "Body   io.Reader")
}

func TestReqFragment_Imports(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		hasBody      bool
		hasTypedBody bool
		wantIO       bool
		wantBytes    bool
		wantJSON     bool
	}{
		{name: "no body", hasBody: false, wantIO: false},
		{name: "with body", hasBody: true, wantIO: true},
		{name: "with typed body", hasBody: true, hasTypedBody: true, wantIO: true, wantBytes: true, wantJSON: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			op := &ir.Operation{HasBody: tt.hasBody, HasTypedBody: tt.hasTypedBody}
			frag := &emit.ReqFragment{Op: op}
			imps := frag.Imports()

			paths := make(map[string]bool)
			for _, imp := range imps {
				paths[imp.Path] = true
			}
			require.Equal(t, tt.wantIO, paths["io"], "io import mismatch")
			require.Equal(t, tt.wantBytes, paths["bytes"], "bytes import mismatch")
			require.Equal(t, tt.wantJSON, paths["encoding/json"], "encoding/json import mismatch")
		})
	}
}

func TestParamsFragment_SimpleOp(t *testing.T) {
	t.Parallel()

	op := &ir.Operation{
		TypePrefix: "ClusterHealth",
		QueryParams: []ir.QueryParam{
			{GoName: "Timeout", WireName: "timeout", GoType: "time.Duration", Kind: ir.ParamDuration, Description: "Request timeout."},
			{GoName: "Local", WireName: "local", GoType: "*bool", Kind: ir.ParamBool},
			{GoName: "Level", WireName: "level", GoType: "string", Kind: ir.ParamString, Default: "cluster"},
		},
	}

	frag := &emit.ParamsFragment{Op: op}

	body, err := frag.Body()
	require.NoError(t, err)

	require.Contains(t, body, "ClusterHealthParams")
	require.Contains(t, body, `formatDuration(r.Timeout)`)
	require.Contains(t, body, `set("local", strconv.FormatBool(*r.Local))`)
	require.Contains(t, body, `set("level", r.Level)`)
	require.Contains(t, body, "// Default: cluster.")
	require.Contains(t, body, "TimeoutParams")
	require.Contains(t, body, "DebugParams")
	require.Contains(t, body, "osparams.EncodeTimeout(r.TimeoutParams, set)")
	require.Contains(t, body, "osparams.EncodeDebug(r.DebugParams, set)")
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

	frag := &emit.ParamsFragment{Op: op}
	imps := frag.Imports()

	paths := make(map[string]bool)
	for _, imp := range imps {
		paths[imp.Path] = true
	}

	require.True(t, paths[emit.LocalModule+"/internal/params"], "missing internal/params import")
	require.True(t, paths["time"], "missing time import for Duration param")
	require.True(t, paths["strconv"], "missing strconv import for Int param")
	require.True(t, paths["strings"], "missing strings import for List param")
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

	f := &emit.File{
		FilePath:  "/tmp/test/cluster-health_gen.go",
		Package:   ir.DefaultCorePkgName,
		Fragments: []emit.Fragment{&emit.ReqFragment{Op: op}, &emit.ParamsFragment{Op: op}},
	}

	src, err := f.Render()
	require.NoError(t, err)

	output := string(src)
	require.Contains(t, output, "package "+ir.DefaultCorePkgName)

	netIdx := strings.Index(output, `"net/http"`)
	buildIdx := strings.Index(output, `"github.com/opensearch-project/opensearch-go/v4/internal/build"`)
	require.Positive(t, netIdx, "missing net/http import")
	require.Positive(t, buildIdx, "missing internal/build import")
	require.Less(t, netIdx, buildIdx, "stdlib imports should precede local module imports")

	require.Contains(t, output, "type ClusterHealthReq struct")
	require.Contains(t, output, "type ClusterHealthParams struct")
}

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
)

func TestParamsTestFragment_Body(t *testing.T) {
	t.Parallel()

	frag := &emit.ParamsTestFragment{
		TypePrefix:  "ClusterHealth",
		HasDuration: true,
		Cases: []emit.ParamTestCase{
			{Name: "timeout", FieldAssign: "Timeout: 5 * time.Second", WantAssign: `"timeout": "5000ms"`},
			{Name: "local=true", FieldAssign: "Local: func(b bool) *bool { return &b }(true)", WantAssign: `"local": "true"`},
			{Name: "local=false", FieldAssign: "Local: func(b bool) *bool { return &b }(false)", WantAssign: `"local": "false"`},
		},
	}

	body, err := frag.Body()
	require.NoError(t, err)

	checks := []struct {
		name string
		want string
	}{
		{name: "func name", want: "func TestClusterHealthParams_get(t *testing.T)"},
		{name: "empty case", want: `name: "empty"`},
		{name: "timeout case", want: `name:   "timeout"`},
		{name: "local true case", want: `name:   "local=true"`},
		{name: "local false case", want: `name:   "local=false"`},
		{name: "false literal", want: "func(b bool) *bool { return &b }(false)"},
		{name: "params type", want: "ClusterHealthParams{"},
		{name: "require.Equal", want: "require.Equal(t, tt.want, tt.params.get())"},
	}

	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Contains(t, body, tc.want)
		})
	}
}

func TestParamsTestFragment_Imports(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		hasDuration bool
		wantTime    bool
	}{
		{name: "with duration", hasDuration: true, wantTime: true},
		{name: "without duration", hasDuration: false, wantTime: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			frag := &emit.ParamsTestFragment{TypePrefix: "X", HasDuration: tt.hasDuration}
			imps := frag.Imports()

			hasTime := false
			for _, imp := range imps {
				if imp.Path == "time" {
					hasTime = true
				}
			}
			require.Equal(t, tt.wantTime, hasTime, "time import mismatch")
		})
	}
}

func TestReqTestFragment_Body(t *testing.T) {
	t.Parallel()

	frag := &emit.ReqTestFragment{
		PkgName:    "osapi",
		ImportPath: "github.com/opensearch-project/opensearch-go/v4/osapi",
		TypePrefix: "ClusterHealth",
		Cases: []emit.ReqTestCase{
			{Name: "empty request", WantMethod: "GET", WantPath: "/_cluster/health", WantErr: "false"},
		},
	}

	body, err := frag.Body()
	require.NoError(t, err)

	checks := []struct {
		name string
		want string
	}{
		{name: "func name", want: "func TestClusterHealthReq_GetRequest(t *testing.T)"},
		{name: "req type", want: "osapi.ClusterHealthReq"},
		{name: "method assertion", want: "require.Equal(t, tt.wantMethod, httpReq.Method)"},
		{name: "path assertion", want: "require.Equal(t, tt.wantPath, httpReq.URL.Path)"},
	}

	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Contains(t, body, tc.want)
		})
	}
}

func TestDispatchTestFragment_Body(t *testing.T) {
	t.Parallel()

	frag := &emit.DispatchTestFragment{
		PkgName:    "osapi",
		ImportPath: "github.com/opensearch-project/opensearch-go/v4/osapi",
		Entries: []emit.DispatchEntry{
			{
				TestName:   "ClusterHealth",
				FieldPath:  "Cluster",
				MethodName: "Health",
				ReqType:    "*osapi.ClusterHealthReq",
				RespType:   "*osapi.ClusterHealthResp",
			},
			{TestName: "Info", FieldPath: "", MethodName: "Info", ReqType: "*osapi.InfoReq", RespType: "*osapi.InfoResp"},
		},
	}

	body, err := frag.Body()
	require.NoError(t, err)

	checks := []struct {
		name string
		want string
	}{
		{name: "sub-client dispatch", want: "c.Cluster.Health"},
		{name: "top-level dispatch", want: "c.Info"},
		{name: "opensearch suppress", want: "var _ = (*opensearch.Response)(nil)"},
	}

	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Contains(t, body, tc.want)
		})
	}
}

func TestNewParamsTestFile_BuildTag(t *testing.T) {
	t.Parallel()

	frag := &emit.ParamsTestFragment{
		TypePrefix: "ClusterHealth",
		Cases:      []emit.ParamTestCase{{Name: "x", FieldAssign: "X: func(b bool) *bool { return &b }(true)", WantAssign: `"x": "true"`}},
	}

	target := emit.NewParamsTestFile("/tmp/test", "osapi", "api_cluster-health", frag)
	src, err := target.Render()
	require.NoError(t, err)

	output := string(src)
	require.Contains(t, output, "//go:build !integration")
	require.Contains(t, output, "package osapi")
}

func TestReqTestFile_BlackBox(t *testing.T) {
	t.Parallel()

	frag := &emit.ReqTestFragment{
		PkgName:    "osapi",
		ImportPath: "github.com/opensearch-project/opensearch-go/v4/osapi",
		TypePrefix: "ClusterHealth",
		Cases:      []emit.ReqTestCase{{Name: "empty", WantMethod: "GET", WantPath: "/", WantErr: "false"}},
	}

	target := &emit.File{
		FilePath:  "/tmp/test/api_cluster-health_gen_test.go",
		Package:   "osapi_test",
		BuildTag:  "!integration",
		Fragments: []emit.Fragment{frag},
	}
	src, err := target.Render()
	require.NoError(t, err)

	output := string(src)
	require.Contains(t, output, "//go:build !integration")
	require.Contains(t, output, "package osapi_test")
}

func TestNewIntegTestFile_BuildTag(t *testing.T) {
	t.Parallel()

	frag := &emit.IntegTestFragment{
		PkgName:    "osapi",
		ImportPath: "github.com/opensearch-project/opensearch-go/v4/osapi",
		ModulePath: "github.com/opensearch-project/opensearch-go/v4",
		CorePkg:    "osapi",
		Config: emit.IntegTestConfig{
			TypePrefix:   "ClusterHealth",
			CallExpr:     "client.Cluster.Health(t.Context(), nil)",
			FailCallExpr: "failingClient.Cluster.Health(t.Context(), nil)",
			CorePkgName:  "osapi",
		},
	}

	target := emit.NewIntegTestFile("/tmp/test", "osapi", "api_cluster-health", frag)
	src, err := target.Render()
	require.NoError(t, err)

	output := string(src)
	require.Contains(t, output, "//go:build integration")
	require.Contains(t, output, "package osapi_test")
	require.Contains(t, output, "func TestClusterHealth(t *testing.T)")
}

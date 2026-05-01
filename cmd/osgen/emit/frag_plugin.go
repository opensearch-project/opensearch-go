// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

// PluginClientOp holds per-operation data needed by the plugin client template.
type PluginClientOp struct {
	MethodName   string
	TypePrefix   string
	IsPointerReq bool
	IsNoBody     bool
	HTTPMethod   string // Go expression for the primary HTTP method (e.g. "http.MethodGet")
}

// PluginClientFragment renders the plugin Client struct, NewClient constructor,
// do() helper, dispatch methods, and noBody sentinel.
type PluginClientFragment struct {
	Ops []PluginClientOp
}

func (f *PluginClientFragment) Imports() []Import {
	return []Import{
		{Path: "context"},
		{Path: "fmt"},
		{Path: "net/http"},
		{Path: "github.com/opensearch-project/opensearch-go/v4"},
	}
}

func (f *PluginClientFragment) Body() (string, error) {
	if len(f.Ops) == 0 {
		return "", nil
	}

	var sb strings.Builder
	if err := pluginClientFragTmpl.Execute(&sb, f.Ops); err != nil {
		return "", fmt.Errorf("rendering PluginClientFragment: %w", err)
	}
	return sb.String(), nil
}

var pluginClientFragTmpl = template.Must(template.New("pluginClient").Parse(`// Client provides methods for this plugin API.
type Client struct {
	Client *opensearch.Client
}

// NewClient creates a new plugin client wrapping the given opensearch.Client.
func NewClient(client *opensearch.Client) *Client {
	return &Client{Client: client}
}

// do calls [opensearch.Do] and checks the response for errors.
func do[T any](ctx context.Context, c *Client, method string, req opensearch.Request, dataPointer *T) (*opensearch.Response, error) {
	resp, err := opensearch.Do(ctx, c.Client, method, req, dataPointer)
	if err != nil {
		return nil, err
	}

	if resp.IsError() {
		if dataPointer != nil {
			return resp, opensearch.ParseError(resp)
		}
		return resp, fmt.Errorf("status: %s", resp.Status())
	}

	return resp, nil
}
{{range .}}
{{- if .IsPointerReq}}
// {{.MethodName}} executes the {{.TypePrefix}} operation.
func (c *Client) {{.MethodName}}(ctx context.Context, req *{{.TypePrefix}}Req) ({{if .IsNoBody}}*opensearch.Response{{else}}*{{.TypePrefix}}Resp{{end}}, error) {
	if req == nil {
		req = &{{.TypePrefix}}Req{}
	}
{{- if .IsNoBody}}
	return do(ctx, c, {{.HTTPMethod}}, *req, noBody)
{{- else}}
	var resp {{.TypePrefix}}Resp
	if _, err := do(ctx, c, {{.HTTPMethod}}, *req, &resp); err != nil {
		return &resp, err
	}
	return &resp, nil
{{- end}}
}
{{- else}}
// {{.MethodName}} executes the {{.TypePrefix}} operation.
func (c *Client) {{.MethodName}}(ctx context.Context, req {{.TypePrefix}}Req) ({{if .IsNoBody}}*opensearch.Response{{else}}*{{.TypePrefix}}Resp{{end}}, error) {
{{- if .IsNoBody}}
	return do(ctx, c, {{.HTTPMethod}}, req, noBody)
{{- else}}
	var resp {{.TypePrefix}}Resp
	if _, err := do(ctx, c, {{.HTTPMethod}}, req, &resp); err != nil {
		return &resp, err
	}
	return &resp, nil
{{- end}}
}
{{- end}}
{{end}}
// noBody is a sentinel used when an operation returns no JSON body.
var noBody *struct{} //nolint:gochecknoglobals
`))

// PluginTestHelperFragment renders the test helper (NewClient + CreateFailingClient)
// for a plugin's internal/test package.
type PluginTestHelperFragment struct {
	Pkg          string
	PluginImport string
	CoreImport   string
	CorePkg      string
}

func (f *PluginTestHelperFragment) Imports() []Import {
	return []Import{
		{Path: "net/http"},
		{Path: "net/http/httptest"},
		{Path: "testing"},
		{Path: "github.com/stretchr/testify/require"},
		{Path: "github.com/opensearch-project/opensearch-go/v4"},
		{Path: f.CoreImport},
		{Path: f.PluginImport},
		{Path: f.CoreImport + "/testutil"},
	}
}

func (f *PluginTestHelperFragment) Body() (string, error) {
	var sb strings.Builder
	if err := pluginTestHelperFragTmpl.Execute(&sb, f); err != nil {
		return "", fmt.Errorf("rendering PluginTestHelperFragment: %w", err)
	}
	return sb.String(), nil
}

var pluginTestHelperFragTmpl = template.Must(template.New("pluginTestHelper").Parse(`// NewClient returns a plugin client connected to the integration test cluster.
func NewClient(t *testing.T) (*{{.Pkg}}.Client, error) {
	t.Helper()
	config := testutil.ClientConfig(t)
	osClient, err := opensearch.NewClient(config.Client)
	if err != nil {
		return nil, err
	}
	return {{.Pkg}}.NewClient(osClient), nil
}

// CreateFailingClient returns a plugin client that always fails with a transport error.
func CreateFailingClient(t *testing.T) (*{{.Pkg}}.Client, error) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Close immediately to cause a transport error.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server does not support hijacking")
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	t.Cleanup(ts.Close)

	osClient, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{ts.URL},
	})
	if err != nil {
		return nil, err
	}
	return {{.Pkg}}.NewClient(osClient), nil
}

// VerifyInspect asserts that a failing-client response has a populated Inspect value.
func VerifyInspect(t *testing.T, inspect {{.CorePkg}}.Inspect) {
	t.Helper()
	require.NotNil(t, inspect.Response, "Inspect().Response must not be nil")
	require.Equal(t, http.StatusBadRequest, inspect.Response.StatusCode)
	require.NotEmpty(t, inspect.Response.Body)
}
`))

// NewPluginClientFile builds a Target for a plugin's client_gen.go.
func NewPluginClientFile(outDir, pkg string, ops []*ir.Operation) Target {
	if len(ops) == 0 {
		return nil
	}

	var clientOps []PluginClientOp
	for _, op := range ops {
		suffix := op.Group
		if idx := strings.IndexByte(suffix, '.'); idx >= 0 {
			suffix = suffix[idx+1:]
		}
		clientOps = append(clientOps, PluginClientOp{
			MethodName:   PluginMethodName(suffix),
			TypePrefix:   op.TypePrefix,
			IsPointerReq: op.IsPointerReq,
			IsNoBody:     op.IsNoBody,
			HTTPMethod:   HTTPMethodConst(PrimaryMethod(op)),
		})
	}

	return &File{
		FilePath:  outDir + "/client_gen.go",
		Package:   pkg,
		Fragments: []Fragment{&PluginClientFragment{Ops: clientOps}},
	}
}

// NewPluginTestHelperFile builds a Target for a plugin's internal/test/helpers_gen.go.
func NewPluginTestHelperFile(outDir, pkg, pluginImport, coreImport, corePkg string) Target {
	testPkg := pkg + "test"
	return &File{
		FilePath: outDir + "/helpers_gen.go",
		Package:  testPkg,
		Fragments: []Fragment{&PluginTestHelperFragment{
			Pkg:          pkg,
			PluginImport: pluginImport,
			CoreImport:   coreImport,
			CorePkg:      corePkg,
		}},
	}
}

// PluginMethodName converts a suffix like "get_alias" to "GetAlias".
func PluginMethodName(suffix string) string {
	parts := strings.FieldsFunc(suffix, func(r rune) bool {
		return r == '_'
	})
	var sb strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			sb.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	return sb.String()
}

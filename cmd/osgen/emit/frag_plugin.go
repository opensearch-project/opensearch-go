// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import (
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

// PluginSubClient describes a sub-client within a plugin package.
type PluginSubClient struct {
	TypeName  string // unexported type name (e.g. "actionGroupClient")
	FieldName string // exported field on Client (e.g. "ActionGroup")
}

// PluginClientOp holds per-operation data needed by the plugin client template.
type PluginClientOp struct {
	MethodName        string
	TypePrefix        string
	IsPointerReq      bool
	IsNoBody          bool
	HTTPMethod        string           // Go expression for the primary HTTP method (e.g. "http.MethodGet")
	SubClient         *PluginSubClient // nil for root Client ops; set for sub-client dispatch
	Group             string
	Description       string
	HTTPMethods       []string
	PrimaryPath       string
	VersionAdded      string
	VersionDeprecated string
	DeprecationMsg    string
	ExcludedDistros   []string
	DocsURL           string
}

// PluginClientFragment renders the plugin Client struct, NewClient constructor,
// do() helper, dispatch methods, and noBody sentinel.
type PluginClientFragment struct {
	Ops        []PluginClientOp
	SubClients []PluginSubClient
}

// Imports returns the imports the plugin Client fragment needs.
func (f *PluginClientFragment) Imports() []Import {
	return []Import{
		{Path: "context"},
		{Path: "fmt"},
		{Path: "net/http"},
		{Path: "github.com/opensearch-project/opensearch-go/v5"},
	}
}

// Body renders the plugin Client struct, its sub-clients, and dispatch methods.
func (f *PluginClientFragment) Body() (string, error) {
	if len(f.Ops) == 0 {
		return "", nil
	}

	// Split ops into root-level and sub-client-dispatched.
	var rootOps, subClientOps []PluginClientOp
	for _, op := range f.Ops {
		if op.SubClient == nil {
			rootOps = append(rootOps, op)
		} else {
			subClientOps = append(subClientOps, op)
		}
	}

	// Build deprecated forwards for sub-client ops (flat method on root Client).
	type deprecatedForward struct {
		PluginClientOp
		SubClientField string
	}
	var deprecatedForwards []deprecatedForward
	for _, op := range subClientOps {
		deprecatedForwards = append(deprecatedForwards, deprecatedForward{
			PluginClientOp: op,
			SubClientField: op.SubClient.FieldName,
		})
	}

	data := struct {
		SubClients         []PluginSubClient
		RootOps            []PluginClientOp
		SubClientOps       []PluginClientOp
		DeprecatedForwards []deprecatedForward
	}{
		SubClients:         f.SubClients,
		RootOps:            rootOps,
		SubClientOps:       subClientOps,
		DeprecatedForwards: deprecatedForwards,
	}

	var sb strings.Builder
	if err := pluginClientFragTmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("rendering PluginClientFragment: %w", err)
	}
	return sb.String(), nil
}

// pluginMethodComment builds a method doc comment from a PluginClientOp.
func pluginMethodComment(op PluginClientOp) string {
	return MethodComment(MethodDocData{
		MethodName:        op.MethodName,
		Group:             op.Group,
		Description:       op.Description,
		HTTPMethods:       op.HTTPMethods,
		PrimaryPath:       op.PrimaryPath,
		VersionAdded:      op.VersionAdded,
		VersionDeprecated: op.VersionDeprecated,
		DeprecationMsg:    op.DeprecationMsg,
		ExcludedDistros:   op.ExcludedDistros,
		DocsURL:           op.DocsURL,
	})
}

//nolint:gochecknoglobals // const-ish read-only template
var pluginClientFragTmpl = template.Must(template.New("pluginClient").Funcs(template.FuncMap{
	"methodComment": pluginMethodComment,
}).Parse(`// Client provides methods for this plugin API.
type Client struct {
	Client *opensearch.Client
{{- range .SubClients}}
	{{.FieldName}} {{.TypeName}}
{{- end}}
}

// NewClient creates a new plugin client wrapping the given opensearch.Client.
func NewClient(client *opensearch.Client) *Client {
{{- if .SubClients}}
	c := &Client{Client: client}
{{- range .SubClients}}
	c.{{.FieldName}} = {{.TypeName}}{client: c}
{{- end}}
	return c
{{- else}}
	return &Client{Client: client}
{{- end}}
}

// do calls [opensearch.Do] and checks the response for errors.
//
// [opensearch.Do] routes through the buffered [opensearchtransport.Client.Perform],
// so resp.Body here is already an [io.NopCloser] over a [bytes.Reader] -- the
// connection has been drained and returned to the pool. The helper only needs
// to translate IsError into a typed error.
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
{{range .SubClients}}
type {{.TypeName}} struct {
	client *Client
}
{{end}}
{{- range .RootOps}}
{{- if .IsPointerReq}}
{{methodComment .}}
func (c *Client) {{.MethodName}}(ctx context.Context, req *{{.TypePrefix}}Req) ({{- ""}}
	{{- if .IsNoBody}}*opensearch.Response{{else}}*{{.TypePrefix}}Resp{{end}}, error) {
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
{{methodComment .}}
func (c *Client) {{.MethodName}}(ctx context.Context, req {{.TypePrefix}}Req) ({{- ""}}
	{{- if .IsNoBody}}*opensearch.Response{{else}}*{{.TypePrefix}}Resp{{end}}, error) {
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
{{- range .SubClientOps}}
{{- if .IsPointerReq}}
{{methodComment .}}
func (c {{.SubClient.TypeName}}) {{.MethodName}}(ctx context.Context, req *{{.TypePrefix}}Req) ({{- ""}}
	{{- if .IsNoBody}}*opensearch.Response{{else}}*{{.TypePrefix}}Resp{{end}}, error) {
	if req == nil {
		req = &{{.TypePrefix}}Req{}
	}
{{- if .IsNoBody}}
	return do(ctx, c.client, {{.HTTPMethod}}, *req, noBody)
{{- else}}
	var resp {{.TypePrefix}}Resp
	if _, err := do(ctx, c.client, {{.HTTPMethod}}, *req, &resp); err != nil {
		return &resp, err
	}
	return &resp, nil
{{- end}}
}
{{- else}}
{{methodComment .}}
func (c {{.SubClient.TypeName}}) {{.MethodName}}(ctx context.Context, req {{.TypePrefix}}Req) ({{- ""}}
	{{- if .IsNoBody}}*opensearch.Response{{else}}*{{.TypePrefix}}Resp{{end}}, error) {
{{- if .IsNoBody}}
	return do(ctx, c.client, {{.HTTPMethod}}, req, noBody)
{{- else}}
	var resp {{.TypePrefix}}Resp
	if _, err := do(ctx, c.client, {{.HTTPMethod}}, req, &resp); err != nil {
		return &resp, err
	}
	return &resp, nil
{{- end}}
}
{{- end}}
{{end}}
{{- range .DeprecatedForwards}}
{{- if .IsPointerReq}}
// Deprecated: use Client.{{.SubClientField}}.{{.MethodName}} instead.
func (c *Client) {{.MethodName}}(ctx context.Context, req *{{.TypePrefix}}Req) ({{- ""}}
	{{- if .IsNoBody}}*opensearch.Response{{else}}*{{.TypePrefix}}Resp{{end}}, error) {
	return c.{{.SubClientField}}.{{.MethodName}}(ctx, req)
}
{{- else}}
// Deprecated: use Client.{{.SubClientField}}.{{.MethodName}} instead.
func (c *Client) {{.MethodName}}(ctx context.Context, req {{.TypePrefix}}Req) ({{- ""}}
	{{- if .IsNoBody}}*opensearch.Response{{else}}*{{.TypePrefix}}Resp{{end}}, error) {
	return c.{{.SubClientField}}.{{.MethodName}}(ctx, req)
}
{{- end}}
{{end}}
// noBody is a sentinel used when an operation returns no JSON body.
var noBody *struct{} //nolint:gochecknoglobals // package-level marker shared by all no-body operations
`))

// PluginTestHelperFragment renders the test helper (NewClient + CreateFailingClient)
// for a plugin's internal/test package.
type PluginTestHelperFragment struct {
	Pkg          string
	PluginImport string
	CoreImport   string
	CorePkg      string
}

// Imports returns the imports the plugin test-helper fragment needs.
func (f *PluginTestHelperFragment) Imports() []Import {
	return []Import{
		{Path: "io"},
		{Path: "net/http"},
		{Path: "net/http/httptest"},
		{Path: "strings"},
		{Path: "testing"},
		{Path: "github.com/stretchr/testify/require"},
		{Path: "github.com/opensearch-project/opensearch-go/v5"},
		{Path: f.CoreImport},
		{Path: f.PluginImport},
		{Path: f.CoreImport + "/testutil"},
	}
}

// Body renders the plugin's test-helper functions (NewClient,
// CreateFailingClient).
func (f *PluginTestHelperFragment) Body() (string, error) {
	var sb strings.Builder
	if err := pluginTestHelperFragTmpl.Execute(&sb, f); err != nil {
		return "", fmt.Errorf("rendering PluginTestHelperFragment: %w", err)
	}
	return sb.String(), nil
}

//nolint:gochecknoglobals // const-ish read-only template
var pluginTestHelperFragTmpl = template.Must(template.New("pluginTestHelper").Parse(
	`// NewClient returns a plugin client connected to the integration test cluster.
func NewClient(t *testing.T) (*{{.Pkg}}.Client, error) {
	t.Helper()
	config := testutil.ClientConfig(t)
	osClient, err := opensearch.NewClient(config.Client)
	if err != nil {
		return nil, err
	}
	return {{.Pkg}}.NewClient(osClient), nil
}

// CreateFailingClient returns a plugin client that always fails with HTTP 400.
//
// The handler writes a real error response body (matching the OpenSearch
// error envelope) so the typed client returns a *opensearch.Response
// populated with status code and body. A transport-layer error
// (hijack-and-close) would leave Inspect().Response nil and break
// VerifyInspect's NotNil assertion.
func CreateFailingClient(t *testing.T) (*{{.Pkg}}.Client, error) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.Copy(w, strings.NewReader("{\"status\": 400, \"error\": \"Test Failing Client Response\"}"))
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
// The byGroup map (group -> *PluginSubClient) determines which ops dispatch
// through a sub-client. Operations not in the map stay flat on Client.
func NewPluginClientFile(outDir, pkg string, ops []*ir.Operation, byGroup map[string]*PluginSubClient) Target {
	if len(ops) == 0 {
		return nil
	}

	// Collect the unique sub-clients in sorted order for deterministic output.
	seen := make(map[string]bool)
	var subClients []PluginSubClient
	for _, op := range ops {
		sc := byGroup[op.Group]
		if sc == nil || seen[sc.FieldName] {
			continue
		}
		seen[sc.FieldName] = true
		subClients = append(subClients, *sc)
	}
	sort.Slice(subClients, func(i, j int) bool {
		return subClients[i].FieldName < subClients[j].FieldName
	})

	var clientOps []PluginClientOp
	for _, op := range ops {
		pco := PluginClientOp{
			MethodName:        op.MethodName,
			TypePrefix:        op.TypePrefix,
			IsPointerReq:      op.IsPointerReq,
			IsNoBody:          op.IsNoBody,
			HTTPMethod:        HTTPMethodConst(PrimaryMethod(op)),
			SubClient:         byGroup[op.Group],
			Group:             op.Group,
			Description:       op.Description,
			HTTPMethods:       op.HTTPMethods,
			PrimaryPath:       op.PrimaryPath,
			VersionAdded:      op.VersionAdded,
			VersionDeprecated: op.VersionDeprecated,
			DeprecationMsg:    op.DeprecationMsg,
			ExcludedDistros:   op.ExcludedDistros,
			DocsURL:           op.DocsURL,
		}
		clientOps = append(clientOps, pco)
	}

	return &File{
		FilePath:  outDir + "/client_gen.go",
		Package:   pkg,
		Fragments: []Fragment{&PluginClientFragment{Ops: clientOps, SubClients: subClients}},
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

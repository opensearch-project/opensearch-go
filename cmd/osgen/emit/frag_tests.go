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
)

// ParamTestCase holds data for one row in the params test table.
type ParamTestCase struct {
	Name        string
	FieldAssign string
	WantAssign  string
}

// ParamsTestFragment renders the _internal_gen_test.go content for Params.get().
type ParamsTestFragment struct {
	TypePrefix string
	// FormatOverride, when non-empty, is the runtime default the SDK emits
	// for the `format` query param when the caller leaves it unset. Mirrors
	// the override applied in ParamsFragment so generated tests anticipate
	// the extra map entry.
	FormatOverride string
	HasDuration    bool
	Cases          []ParamTestCase
}

func (f *ParamsTestFragment) Imports() []Import {
	imps := []Import{
		{Path: "testing"},
		{Path: "github.com/stretchr/testify/require"},
	}
	if f.HasDuration {
		imps = append(imps, Import{Path: "time"})
	}
	return imps
}

func (f *ParamsTestFragment) Body() (string, error) {
	if len(f.Cases) == 0 && f.TypePrefix == "" {
		return "", nil
	}

	var sb strings.Builder
	if err := paramsTestFragTmpl.Execute(&sb, f); err != nil {
		return "", fmt.Errorf("rendering ParamsTestFragment for %s: %w", f.TypePrefix, err)
	}
	return sb.String(), nil
}

var paramsTestFragTmpl = template.Must(template.New("paramsTest").Funcs(template.FuncMap{
	"quote": func(s string) string { return fmt.Sprintf("%q", s) },
}).Parse(`func Test{{.TypePrefix}}Params_get(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		params {{.TypePrefix}}Params
		want   map[string]string
	}{
{{- if .FormatOverride}}
		{name: "empty", params: {{.TypePrefix}}Params{}, want: map[string]string{"format": {{quote .FormatOverride}}}},
{{- else}}
		{name: "empty", params: {{.TypePrefix}}Params{}, want: nil},
{{- end}}
{{- range .Cases}}
		{
			name:   {{quote .Name}},
			params: {{$.TypePrefix}}Params{ {{.FieldAssign}} },
			want:   map[string]string{ {{if $.FormatOverride}}"format": {{quote $.FormatOverride}}, {{end}}{{.WantAssign}} },
		},
{{- end}}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.params.get())
		})
	}
}
`))

// ReqTestCase holds data for one row in the GetRequest test table.
type ReqTestCase struct {
	Name        string
	FieldAssign string
	WantMethod  string
	WantPath    string
	WantErr     string
}

// ReqTestFragment renders the _gen_test.go content (black-box) for Req.GetRequest().
type ReqTestFragment struct {
	PkgName      string
	ImportPath   string
	TypePrefix   string
	NeedsStrings bool
	Cases        []ReqTestCase
}

func (f *ReqTestFragment) Imports() []Import {
	imps := []Import{
		{Path: "testing"},
		{Path: "github.com/stretchr/testify/require"},
		{Path: f.ImportPath},
	}
	if f.NeedsStrings {
		imps = append(imps, Import{Path: "strings"})
	}
	return imps
}

func (f *ReqTestFragment) Body() (string, error) {
	if len(f.Cases) == 0 {
		return "", nil
	}

	var sb strings.Builder
	if err := reqTestFragTmpl.Execute(&sb, f); err != nil {
		return "", fmt.Errorf("rendering ReqTestFragment for %s: %w", f.TypePrefix, err)
	}
	return sb.String(), nil
}

var reqTestFragTmpl = template.Must(template.New("reqTest").Funcs(template.FuncMap{
	"quote": func(s string) string { return fmt.Sprintf("%q", s) },
}).Parse(`func Test{{.TypePrefix}}Req_GetRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		req        {{.PkgName}}.{{.TypePrefix}}Req
		wantMethod string
		wantPath   string
		wantErr    bool
	}{
{{- range .Cases}}
		{
			name:       {{quote .Name}},
			req:        {{$.PkgName}}.{{$.TypePrefix}}Req{ {{.FieldAssign}} },
			wantMethod: {{quote .WantMethod}},
			wantPath:   {{quote .WantPath}},
			wantErr:    {{.WantErr}},
		},
{{- end}}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			httpReq, err := tt.req.GetRequest(tt.wantMethod)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantMethod, httpReq.Method)
			require.Equal(t, tt.wantPath, httpReq.URL.Path)
		})
	}
}
`))

// DispatchEntry holds data for one dispatch signature test function.
type DispatchEntry struct {
	TestName   string
	FieldPath  string
	MethodName string
	ReqType    string
	RespType   string
}

// DispatchTestFragment renders compile-time dispatch signature assertions.
type DispatchTestFragment struct {
	PkgName    string
	ImportPath string
	Entries    []DispatchEntry
}

func (f *DispatchTestFragment) Imports() []Import {
	return []Import{
		{Path: "context"},
		{Path: "testing"},
		{Path: "github.com/opensearch-project/opensearch-go/v4"},
		{Path: f.ImportPath},
	}
}

func (f *DispatchTestFragment) Body() (string, error) {
	if len(f.Entries) == 0 {
		return "", nil
	}

	var sb strings.Builder
	if err := dispatchTestFragTmpl.Execute(&sb, f); err != nil {
		return "", fmt.Errorf("rendering DispatchTestFragment: %w", err)
	}
	return sb.String(), nil
}

var dispatchTestFragTmpl = template.Must(template.New("dispatchTest").Parse(`// suppress unused import
var _ = (*opensearch.Response)(nil)
{{range .Entries}}
func TestDispatch_{{.TestName}}(t *testing.T) {
	// Compile-time signature assertion.
	var c {{$.PkgName}}.Client
	var _ func(context.Context, {{.ReqType}}) ({{.RespType}}, error) = {{if .FieldPath}}c.{{.FieldPath}}.{{.MethodName}}{{else}}c.{{.MethodName}}{{end}}
}
{{end}}`))

// IntegTestConfig holds pre-classified data for a single generated integration
// test function. All fields are computed from the operation's structural metadata
// (path fields, fixture kind, HTTP method) during classifyOpIR.
type IntegTestConfig struct {
	// TypePrefix is the PascalCase operation name used for the test function
	// name and type references (e.g. "ClusterHealth", "IndicesPutAlias").
	TypePrefix string

	// VersionAdded is the OpenSearch version that introduced this operation
	// (e.g. "2.4.0"). When non-empty, the test emits a SkipIfVersion guard.
	// Empty string means the operation exists in all supported versions.
	VersionAdded string

	// ResourcePrefix is the kebab-case prefix passed to MustUniqueString for
	// generating unique resource names (index, docID, name). Derived from
	// TypePrefix (e.g. "test-cluster-health"). Empty means the test creates
	// no server-side resources and needs no unique identifiers.
	ResourcePrefix string

	// FixtureCode is the Go source that creates prerequisite resources (index,
	// document, template, alias, etc.) before the test exercises the operation.
	// Empty means the operation needs no prerequisites.
	FixtureCode string

	// SkipReason, when non-empty, causes the test to call t.Skip with this
	// message. Used for operations that cannot be safely tested in CI (e.g.
	// cluster decommission, snapshot operations requiring external repos).
	SkipReason string

	// CallExpr is the Go expression that invokes the operation under test
	// (e.g. "client.Cluster.Health(t.Context(), osapi.ClusterHealthReq{...})").
	// References variables declared by the template: index, docID, name.
	CallExpr string

	// FailCallExpr is the same as CallExpr but uses failingClient to exercise
	// the Inspect() error path.
	FailCallExpr string

	// NeedsIndex is true when the test requires an `index` variable. Set when
	// the operation has a fixture (all fixtures create or reference an index
	// for cleanup) or when the operation has a required Index path field.
	NeedsIndex bool

	// NeedsDocID is true when the test requires a `docID` variable. Set when
	// the operation has a required ID or DocumentID path field.
	NeedsDocID bool

	// NeedsName is true when the test requires a `name` variable. Set when
	// the operation has a required path field that is not Index or ID (e.g.
	// Name, Alias), or when the fixture kind creates a named resource
	// (template, alias, pipeline).
	NeedsName bool

	// IsNoBody is true when the operation returns *opensearch.Response (raw
	// HTTP response) rather than a typed Resp struct. Affects which assertions
	// the test uses.
	IsNoBody bool

	// IsPlugin is true when the operation belongs to a plugin package rather
	// than the core osapi package. Affects client construction and imports.
	IsPlugin bool

	// CorePkgName is the package name for the core API types (e.g. "osapi").
	// Used in type references like "osapi.IndicesDeleteReq" within fixture and
	// cleanup code.
	CorePkgName string
}

// IntegTestFragment renders one integration test function.
type IntegTestFragment struct {
	PkgName    string
	ImportPath string
	ModulePath string
	CorePkg    string
	Config     IntegTestConfig
}

func (f *IntegTestFragment) Imports() []Import {
	cfg := f.Config
	var imps []Import

	if cfg.FixtureCode != "" {
		imps = append(imps, Import{Path: "context"})
	}

	imps = append(imps, Import{Path: "testing"})
	imps = append(imps, Import{Path: "github.com/stretchr/testify/require"})

	if cfg.IsPlugin {
		imps = append(imps, Import{Path: f.ImportPath + "/internal/test", Alias: "plugintest"})
	}

	needsStrings := strings.Contains(cfg.CallExpr, "strings.NewReader") || strings.Contains(cfg.FixtureCode, "strings.NewReader")
	if needsStrings {
		imps = append(imps, Import{Path: "strings"})
	}

	needsTime := strings.Contains(cfg.CallExpr, "time.") || strings.Contains(cfg.FixtureCode, "time.")
	if needsTime {
		imps = append(imps, Import{Path: "time"})
	}

	needImportPkg := cfg.FixtureCode != "" || strings.Contains(cfg.CallExpr, f.PkgName+".")
	if needImportPkg {
		imps = append(imps, Import{Path: f.ImportPath})
	}

	if cfg.IsPlugin && (strings.Contains(cfg.FixtureCode, f.CorePkg+".") || strings.Contains(cfg.CallExpr, f.CorePkg+".")) {
		imps = append(imps, Import{Path: f.ModulePath + "/" + f.CorePkg})
	}

	needTestutil := !cfg.IsPlugin || cfg.VersionAdded != "" || !cfg.IsNoBody || cfg.FixtureCode != "" ||
		cfg.NeedsIndex || cfg.NeedsDocID || cfg.NeedsName
	if needTestutil {
		imps = append(imps, Import{Path: f.ModulePath + "/" + f.CorePkg + "/testutil"})
	}

	if !cfg.IsNoBody && !cfg.IsPlugin {
		imps = append(imps, Import{Path: f.ModulePath + "/" + f.CorePkg + "/internal/test", Alias: "osapitest"})
	}

	return imps
}

func (f *IntegTestFragment) Body() (string, error) {
	var sb strings.Builder
	if err := integTestFragTmpl.Execute(&sb, f); err != nil {
		return "", fmt.Errorf("rendering IntegTestFragment for %s: %w", f.Config.TypePrefix, err)
	}
	return sb.String(), nil
}

var integTestFragTmpl = template.Must(template.New("integTest").Funcs(template.FuncMap{
	"quote": func(s string) string { return fmt.Sprintf("%q", s) },
}).Parse(`func Test{{.Config.TypePrefix}}(t *testing.T) {
{{- if .Config.SkipReason}}
	t.Skip({{quote .Config.SkipReason}}) //nolint:gocritic // FIXME: implement proper test fixture
{{- end}}
{{- if .Config.IsPlugin}}
	client, err := plugintest.NewClient(t)
{{- else}}
	client, err := testutil.NewClient(t)
{{- end}}
	require.NoError(t, err)
{{- if and .Config.IsPlugin (or (ne .Config.VersionAdded "") .Config.FixtureCode)}}

	osClient, err := testutil.NewClient(t)
	require.NoError(t, err)
{{- end}}
{{- if .Config.VersionAdded}}

{{- if .Config.IsPlugin}}
	testutil.SkipIfVersion(t, osClient, "<", {{quote .Config.VersionAdded}}, {{quote .Config.TypePrefix}})
{{- else}}
	testutil.SkipIfVersion(t, client, "<", {{quote .Config.VersionAdded}}, {{quote .Config.TypePrefix}})
{{- end}}
{{- end}}
{{- if .Config.NeedsIndex}}

	index := testutil.MustUniqueString(t, {{quote .Config.ResourcePrefix}})
{{- end}}
{{- if .Config.NeedsDocID}}
	docID := testutil.MustUniqueString(t, {{quote .Config.ResourcePrefix}})
{{- end}}
{{- if .Config.NeedsName}}
	name := testutil.MustUniqueString(t, {{quote .Config.ResourcePrefix}})
{{- end}}
{{- if .Config.FixtureCode}}
	t.Cleanup(func() {
{{- if .Config.IsPlugin}}
		_, _ = osClient.Indices.Delete(context.Background(), &{{.Config.CorePkgName}}.IndicesDeleteReq{Index: []string{index}})
{{- else}}
		_, _ = client.Indices.Delete(context.Background(), &{{.Config.CorePkgName}}.IndicesDeleteReq{Index: []string{index}})
{{- end}}
	})

	{{.Config.FixtureCode}}
{{- end}}

	t.Run("success", func(t *testing.T) {
{{- if .Config.IsNoBody}}
		resp, err := {{.Config.CallExpr}}
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Greater(t, resp.StatusCode, 0)
{{- else}}
		resp, err := {{.Config.CallExpr}}
		require.NoError(t, err)
		require.NotNil(t, resp)
		testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
{{- end}}
	})
{{- if not .Config.IsNoBody}}

	t.Run("inspect", func(t *testing.T) {
{{- if .Config.IsPlugin}}
		failingClient, err := plugintest.CreateFailingClient(t)
{{- else}}
		failingClient, err := osapitest.CreateFailingClient(t)
{{- end}}
		require.NoError(t, err)

		res, err := {{.Config.FailCallExpr}}
		require.Error(t, err)
		require.NotNil(t, res)
{{- if .Config.IsPlugin}}
		plugintest.VerifyInspect(t, res.Inspect())
{{- else}}
		osapitest.VerifyInspect(t, res.Inspect())
{{- end}}
	})
{{- end}}
}
`))

// NewParamsTestFile builds a Target for <basename>_internal_gen_test.go.
func NewParamsTestFile(outDir, pkg, basename string, frag *ParamsTestFragment) Target {
	return &File{
		FilePath:   outDir + "/" + basename + "_internal_gen_test.go",
		Package:    pkg,
		BuildTag:   "!integration",
		Fragments:  []Fragment{frag},
	}
}

// NewDispatchTestFile builds a Target for dispatch_gen_test.go.
func NewDispatchTestFile(outDir, pkg string, frag *DispatchTestFragment) Target {
	return &File{
		FilePath:   outDir + "/dispatch_gen_test.go",
		Package:    pkg + "_test",
		BuildTag:   "!integration",
		Fragments:  []Fragment{frag},
	}
}

// NewIntegTestFile builds a Target for <basename>_integ_gen_test.go.
func NewIntegTestFile(outDir, pkg, basename string, frag *IntegTestFragment) Target {
	return &File{
		FilePath:   outDir + "/" + basename + "_integ_gen_test.go",
		Package:    pkg + "_test",
		BuildTag:   "integration",
		Fragments:  []Fragment{frag},
	}
}

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

// RoundtripTestFragment renders an httptest-based roundtrip test that exercises
// the full dispatch method -> request() -> opensearch.Execute() -> unmarshal pipeline.
type RoundtripTestFragment struct {
	PkgName    string
	ImportPath string
	TypePrefix string

	// RespFixture is the JSON returned by the mock server ("[]", "{}", or "").
	RespFixture string

	// IsNoBody is true when the operation returns *opensearch.Response.
	IsNoBody bool

	// IsPlugin selects the client construction style: plugin clients wrap an
	// opensearch.Client (pkg.NewClient(osClient)), whereas the core client takes
	// a config (pkg.NewClient(pkg.Config{...})).
	IsPlugin bool

	// CallExpr is the Go expression invoking the operation (e.g. "client.Cat.Nodes(t.Context(), nil)").
	CallExpr string

	// ErrCallExpr is the same as CallExpr but against errClient.
	ErrCallExpr string

	// NeedsBody is true when the request requires a body to avoid validation errors.
	NeedsBody bool

	// NeedsStrings is true when the call expression uses strings.NewReader.
	NeedsStrings bool
}

// Imports returns the imports the round-trip test fragment needs.
func (f *RoundtripTestFragment) Imports() []Import {
	imps := []Import{
		{Path: "io"},
		{Path: "net/http"},
		{Path: "net/http/httptest"},
		{Path: "testing"},
		{Path: "github.com/stretchr/testify/require"},
		{Path: "github.com/opensearch-project/opensearch-go/v5"},
		{Path: f.ImportPath},
	}
	if f.NeedsStrings {
		imps = append(imps, Import{Path: "strings"})
	}
	return imps
}

// Body renders the round-trip test function for the operation.
func (f *RoundtripTestFragment) Body() (string, error) {
	var sb strings.Builder
	if err := roundtripTestFragTmpl.Execute(&sb, f); err != nil {
		return "", fmt.Errorf("rendering RoundtripTestFragment for %s: %w", f.TypePrefix, err)
	}
	return sb.String(), nil
}

//nolint:gochecknoglobals // const-ish read-only template
var roundtripTestFragTmpl = template.Must(template.New("roundtripTest").Funcs(template.FuncMap{
	"quote": func(s string) string { return fmt.Sprintf("%q", s) },
}).Parse(`func Test{{.TypePrefix}}_Roundtrip(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
{{- if .RespFixture}}
			_, _ = io.WriteString(w, ` + "`" + `{{.RespFixture}}` + "`" + `)
{{- end}}
		}))
		t.Cleanup(ts.Close)
{{if .IsPlugin}}
		osClient, err := opensearch.NewClient(opensearch.Config{Addresses: []string{ts.URL}})
		require.NoError(t, err)
		client := {{.PkgName}}.NewClient(osClient)
{{- else}}
		client, err := {{.PkgName}}.NewClient({{.PkgName}}.Config{
			Client: opensearch.Config{Addresses: []string{ts.URL}},
		})
		require.NoError(t, err)
{{- end}}

		resp, err := {{.CallExpr}}
		require.NoError(t, err)
		require.NotNil(t, resp)
{{- if .IsNoBody}}
		require.Greater(t, resp.StatusCode, 0)
{{- else}}
		require.NotNil(t, resp.Inspect().Response)
{{- end}}
	})

	t.Run("error", func(t *testing.T) {
		t.Parallel()
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, ` + "`" + `{"status":400,"error":{"reason":"test error","type":"invalid_request"}}` + "`" + `)
		}))
		t.Cleanup(ts.Close)
{{if .IsPlugin}}
		osClient, err := opensearch.NewClient(opensearch.Config{Addresses: []string{ts.URL}})
		require.NoError(t, err)
		errClient := {{.PkgName}}.NewClient(osClient)
{{- else}}
		errClient, err := {{.PkgName}}.NewClient({{.PkgName}}.Config{
			Client: opensearch.Config{Addresses: []string{ts.URL}},
		})
		require.NoError(t, err)
{{- end}}

		resp, err := {{.ErrCallExpr}}
		require.Error(t, err)
		require.NotNil(t, resp)
	})
}
`))

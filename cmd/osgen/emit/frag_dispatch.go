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

// DispatchFragment renders one or more client dispatch methods for an operation.
type DispatchFragment struct {
	Op *ir.Operation
}

func (f *DispatchFragment) Imports() []Import {
	if len(f.Op.DispatchRoutes) == 0 {
		return nil
	}
	imps := []Import{
		{Path: "context"},
		{Path: "net/http"},
	}
	if f.Op.IsNoBody {
		imps = append(imps, Import{Path: LocalModule, Alias: "opensearch"})
	}
	return imps
}

func (f *DispatchFragment) Body() (string, error) {
	if len(f.Op.DispatchRoutes) == 0 {
		return "", nil
	}

	data := struct {
		*ir.Operation
		Routes []ir.DispatchRoute
	}{
		Operation: f.Op,
		Routes:    f.Op.DispatchRoutes,
	}

	var sb strings.Builder
	if err := dispatchTmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("rendering DispatchFragment for %s: %w", f.Op.Group, err)
	}
	return sb.String(), nil
}

var dispatchTmpl = template.Must(template.New("dispatch").Funcs(template.FuncMap{
	"methodConst":   HTTPMethodConst,
	"primaryMethod": PrimaryMethod,
}).Parse(
	`{{- range .Routes}}
{{- if .Deprecated}}
// Deprecated: use {{$.TypePrefix}} via the parent client instead.
{{- end}}
func (c {{.ReceiverType}}) {{.MethodName}}(ctx context.Context, req {{if $.IsPointerReq}}*{{end}}{{$.TypePrefix}}Req) ({{if $.IsNoBody}}*opensearch.Response{{else}}*{{$.TypePrefix}}Resp{{end}}, error) {
{{- if $.IsPointerReq}}
	if req == nil {
		req = &{{$.TypePrefix}}Req{}
	}
{{end}}
{{- if $.IsNoBody}}
	return do(ctx, {{if .TopLevel}}&c{{else}}c.apiClient{{end}}, {{methodConst (primaryMethod $.Operation)}}, req, noBody)
{{- else}}
	var (
		data {{$.TypePrefix}}Resp
		err  error
	)
	if data.response, err = do(ctx, {{if .TopLevel}}&c{{else}}c.apiClient{{end}}, {{methodConst (primaryMethod $.Operation)}}, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
{{- end}}
}
{{end}}`))

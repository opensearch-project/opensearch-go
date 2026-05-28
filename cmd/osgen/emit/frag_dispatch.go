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

// Imports returns the imports the dispatch-method fragment needs.
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

// Body renders the client method that dispatches a request to the operation.
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

//nolint:gochecknoglobals // const-ish read-only template
var dispatchTmpl = template.Must(template.New("dispatch").Funcs(template.FuncMap{
	"methodConst":      HTTPMethodConst,
	"primaryMethod":    PrimaryMethod,
	"bodyMethodSwitch": bodyMethodSwitch,
	"opMethodComment": func(methodName string, op *ir.Operation) string {
		return MethodComment(MethodDocData{
			MethodName:        methodName,
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
	},
}).Parse(
	`{{- range .Routes}}
{{- if .Deprecated}}
// Deprecated: use {{$.TypePrefix}} via the parent client instead.
{{- else}}
{{opMethodComment .MethodName $.Operation}}
{{- end}}
func (c {{.ReceiverType}}) {{.MethodName}}(ctx context.Context, req {{if $.IsPointerReq}}*{{end}}{{$.TypePrefix}}Req) ({{- ""}}
	{{- if $.IsNoBody}}*opensearch.Response{{else}}*{{$.TypePrefix}}Resp{{end}}, error) {
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
{{- if bodyMethodSwitch $.Operation}}
	method := {{methodConst (primaryMethod $.Operation)}}
	if req.Body != nil{{if $.HasTypedBody}} || req.BodyReader != nil{{end}} {
		method = {{methodConst (bodyMethodSwitch $.Operation)}}
	}
	if data.response, err = do( {{- ""}}
		ctx,
		{{if .TopLevel}}&c{{else}}c.apiClient{{end}},
		method,
		req, &data,
	); err != nil {
		return &data, err
	}
{{- else}}
	if data.response, err = do( {{- ""}}
		ctx,
		{{if .TopLevel}}&c{{else}}c.apiClient{{end}},
		{{methodConst (primaryMethod $.Operation)}},
		req, &data,
	); err != nil {
		return &data, err
	}
{{- end}}

	return &data, nil
{{- end}}
}
{{end}}`))

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import (
	"fmt"
	"net/http"
	"strings"
	"text/template"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

// ReqFragment renders the Req struct and its GetRequest() method.
type ReqFragment struct {
	Op *ir.Operation
}

func (f *ReqFragment) Imports() []Import {
	imps := []Import{
		{Path: "net/http"},
		{Path: LocalModule, Alias: "opensearch"},
		{Path: LocalModule + "/internal/path", Alias: "ospath"},
	}
	if f.Op.HasBody {
		imps = append(imps, Import{Path: "io"})
	}
	return imps
}

func (f *ReqFragment) Body() (string, error) {
	var sb strings.Builder
	if err := reqTmpl.Execute(&sb, f.Op); err != nil {
		return "", fmt.Errorf("rendering ReqFragment for %s: %w", f.Op.Group, err)
	}
	return sb.String(), nil
}

// bodyMethodSwitch returns the HTTP method to use when a request body is
// present, if it differs from the primary method. For operations that support
// both GET and POST and accept a body, the caller should use POST when a body
// is provided. Returns "" if no switch applies.
func bodyMethodSwitch(op *ir.Operation) string {
	if !op.HasBody {
		return ""
	}
	primary := PrimaryMethod(op)
	if primary != http.MethodGet {
		return ""
	}
	for _, m := range op.HTTPMethods {
		if m == http.MethodPost {
			return http.MethodPost
		}
	}
	return ""
}

// PrimaryMethod returns the first HTTP method listed for the operation.
func PrimaryMethod(op *ir.Operation) string {
	if len(op.HTTPMethods) == 0 {
		return ""
	}
	return op.HTTPMethods[0]
}

var reqTmpl = template.Must(template.New("req").Funcs(template.FuncMap{
	"join":             strings.Join,
	"comment":          CommentWrap,
	"wrapLine":         WrapLine,
	"availabilityNote": AvailabilityNote,
}).Parse(`// {{.TypePrefix}}Req represents the request for the {{.Group}} operation.
{{- if .Description}}
{{comment .Description}}
{{- end}}
//
{{- if gt (len .HTTPMethods) 1}}
// Path: {{.PrimaryPath}}
//
// Methods: {{join .HTTPMethods ", "}}
{{- else}}
// {{index .HTTPMethods 0}} {{.PrimaryPath}}
{{- end}}
{{- with availabilityNote .VersionAdded .VersionDeprecated .DeprecationMsg}}
{{wrapLine .}}
{{- end}}
{{- if .ExcludedDistros}}
//
// Not available on: {{join .ExcludedDistros ", "}}.
{{- end}}
{{- if .DocsURL}}
//
// See: {{.DocsURL}}
{{- end}}
type {{.TypePrefix}}Req struct {
{{- range $i, $f := .PathFields}}
{{- if $i}}
{{end}}
	// {{$f.GoName}} specifies the {{if $f.IsList}}list of path segments{{else}}path segment{{end}} for the request URL.
	{{$f.GoName}} {{if $f.IsList}}[]string{{else}}string{{end}}
{{- end}}
{{- if .HasBody}}
{{if .PathFields}}
{{end}}
	// Body is the request payload, typically JSON-encoded.
	Body   io.Reader
{{- end}}
{{if or .PathFields .HasBody}}
{{end}}
	// Header provides additional HTTP headers for the request.
	Header http.Header

	// Params holds optional query parameters for the request.
	Params {{.TypePrefix}}Params
}

// GetRequest builds the HTTP request from the structured fields.
func (r {{.TypePrefix}}Req) GetRequest(method string) (*http.Request, error) {
	path, err := ospath.{{.PathBuilder.StructName}}{
{{- range .PathFields}}
		{{.GoName}}: r.{{.GoName}},
{{- end}}
	}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(
		method,
		path,
{{- if .HasBody}}
		r.Body,
{{- else}}
		nil,
{{- end}}
		r.Params.get(),
		r.Header,
	)
}
`))

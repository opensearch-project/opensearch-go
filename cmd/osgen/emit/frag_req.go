// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"text/template"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

// ReqFragment renders the Req struct and its GetRequest() method.
type ReqFragment struct {
	Op       *ir.Operation
	Registry *ir.TypeRegistry
}

// Imports returns the imports the Req fragment needs.
func (f *ReqFragment) Imports() []Import {
	imps := []Import{
		{Path: "net/http"},
		{Path: LocalModule + "/internal/build"},
		{Path: LocalModule + "/internal/path", Alias: "ospath"},
	}
	if f.Op.HasTypedBody {
		imps = append(imps,
			Import{Path: "bytes"},
			Import{Path: "encoding/json"},
			Import{Path: "io"},
		)
		if f.Op.IsPlugin && f.Registry != nil && f.Op.RequestBody != nil &&
			f.Op.RequestBody.Scope == ir.ScopeShared {
			imps = append(imps, Import{Path: f.Registry.CoreImport})
		}
	} else if f.Op.HasBody {
		imps = append(imps, Import{Path: "io"})
	}
	return imps
}

// Body renders the Req struct (and its associated path/body builders) for
// the operation.
func (f *ReqFragment) Body() (string, error) {
	qualify := qualifierFunc(f.Op.IsPlugin, f.Registry)

	tmpl := template.Must(template.New("req").Funcs(template.FuncMap{
		"join":             strings.Join,
		"comment":          CommentWrap,
		"wrapLine":         WrapLine,
		"availabilityNote": AvailabilityNote,
		"qualify":          qualify,
		"hasSensitiveBody": hasSensitiveBody,
	}).Parse(reqTmplStr))

	var sb strings.Builder
	if err := tmpl.Execute(&sb, f.Op); err != nil {
		return "", fmt.Errorf("rendering ReqFragment for %s: %w", f.Op.Group, err)
	}
	return sb.String(), nil
}

// sensitiveBodyOps lists operation groups whose request body contains a
// Password (or other credential) field whose JSON tag matches gosec's G117
// "secret pattern" detector. The Marshal call site for these operations is
// annotated with //nolint:gosec so the legitimate-credential marshal isn't
// flagged.
//
//nolint:gochecknoglobals // immutable allowlist consulted by the template
var sensitiveBodyOps = map[string]struct{}{
	"security.change_password":    {},
	"security.create_user":        {},
	"security.create_user_legacy": {},
}

// hasSensitiveBody reports whether the operation marshals a body that
// contains a credential field; used by the Req template to suppress
// gosec G117 on the json.Marshal call site.
func hasSensitiveBody(op *ir.Operation) bool {
	_, ok := sensitiveBodyOps[op.Group]
	return ok
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
	if slices.Contains(op.HTTPMethods, http.MethodPost) {
		return http.MethodPost
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

const reqTmplStr = `// {{.TypePrefix}}Req represents the request for the {{.Group}} operation.
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
{{- if .HasTypedBody}}
{{if .PathFields}}
{{end}}
	// Body specifies the typed request body. When non-nil, it is
	// marshaled to JSON for the request payload.
	Body *{{qualify .RequestBody.Name}}

	// BodyReader provides an escape hatch for sending a raw request
	// body. It is used only when Body is nil.
	BodyReader io.Reader
{{- else if .HasBody}}
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
	Params *{{.TypePrefix}}Params
}

// GetRequest builds the HTTP request from the structured fields.
func (r {{.TypePrefix}}Req) GetRequest(method string) (*http.Request, error) {
{{- if .Deprecated}}
	// SA1019 is suppressed: this Req is the canonical caller for a deprecated
	// operation; the path builder is deprecated for the same reason.
	path, err := ospath.{{.PathBuilder.StructName}}{ //nolint:staticcheck // operation deprecated; intentional consumer of deprecated builder
{{- else}}
	path, err := ospath.{{.PathBuilder.StructName}}{
{{- end}}
{{- range .PathFields}}
		{{.GoName}}: r.{{.GoName}},
{{- end}}
	}.Build()
	if err != nil {
		return nil, err
	}

	var params map[string]string
	if r.Params != nil {
		params = r.Params.get()
	}

{{- if .IsNDJSON}}

	// _bulk and _msearch take application/x-ndjson; build.Request defaults
	// to application/json when the caller leaves Content-Type unset, so
	// we set it here. Caller-provided Content-Type still wins.
	headers := r.Header
	if headers.Get(build.HeaderContentType) == "" {
		headers = headers.Clone()
		if headers == nil {
			headers = make(http.Header, 1)
		}
		headers.Set(build.HeaderContentType, build.ContentTypeNDJSON)
	}

{{- end}}
{{- if .HasTypedBody}}

	var bodyReader io.Reader
	if r.Body != nil {
{{- if hasSensitiveBody .}}
		// Body contains a credential field whose JSON tag (e.g. "password")
		// matches gosec G117. The marshal here intentionally serializes the
		// credential because the OpenSearch security API requires it on the
		// wire; suppress the lint at the call site.
		bodyData, err := json.Marshal(r.Body) //nolint:gosec // G117: legitimate credential field for security API
{{- else}}
		bodyData, err := json.Marshal(r.Body)
{{- end}}
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(bodyData)
	} else if r.BodyReader != nil {
		bodyReader = r.BodyReader
	}

	return build.Request(
		method,
		path,
		bodyReader,
		params,
{{- if .IsNDJSON}}
		headers,
{{- else}}
		r.Header,
{{- end}}
	)
{{- else}}

	return build.Request(
		method,
		path,
{{- if .HasBody}}
		r.Body,
{{- else}}
		nil,
{{- end}}
		params,
{{- if .IsNDJSON}}
		headers,
{{- else}}
		r.Header,
{{- end}}
	)
{{- end}}
}
`

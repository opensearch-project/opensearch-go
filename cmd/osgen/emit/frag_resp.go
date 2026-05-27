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

// RespFragment renders the Resp struct and its Inspect() method.
type RespFragment struct {
	Op       *ir.Operation
	Registry *ir.TypeRegistry
}

// Imports returns the imports the Resp fragment needs.
func (f *RespFragment) Imports() []Import {
	if f.Op.IsNoBody {
		return nil
	}
	imps := []Import{
		{Path: "bytes"},
		{Path: "io"},
		{Path: LocalModule, Alias: "opensearch"},
	}

	// The raw-body variant references build.NullJSON in MarshalJSON.
	if f.Op.RespShape == ir.RespShapeRaw {
		imps = append(imps, Import{Path: LocalModule + "/internal/build"})
	}

	// Non-struct shapes always need encoding/json for custom marshal/unmarshal.
	if f.Op.RespShape != ir.RespShapeStruct {
		imps = append(imps, Import{Path: "encoding/json"})
		if f.Op.IsPlugin && f.Op.RespElemType != nil && f.Op.RespElemType.Scope == ir.ScopeShared {
			imps = append(imps, Import{Path: f.Registry.CoreImport})
		}
		return imps
	}

	if f.hasJSONRaw() {
		imps = append(imps, Import{Path: "encoding/json"})
	}
	if f.Op.IsPlugin && f.hasCrossPackageTypes() {
		imps = append(imps, Import{Path: f.Registry.CoreImport})
	}
	return imps
}

// Body renders the Resp struct (and its UnmarshalJSON when needed).
func (f *RespFragment) Body() (string, error) {
	if f.Op.IsNoBody || f.Op.Response == nil {
		return "", nil
	}

	// Non-struct shapes get their own dedicated templates.
	switch f.Op.RespShape {
	case ir.RespShapeMap:
		return f.renderMapResp()
	case ir.RespShapeArray:
		return f.renderArrayResp()
	case ir.RespShapeRaw:
		return f.renderRawResp()
	case ir.RespShapeStruct:
		// fall through to the default struct template
	}

	qualify := qualifierFunc(f.Op.IsPlugin, f.Registry)
	data := struct {
		*ir.Operation
		RespFields []ir.Field
	}{
		Operation:  f.Op,
		RespFields: f.Op.Response.Fields,
	}

	tmpl := template.Must(template.New("resp").Funcs(template.FuncMap{
		"comment":          CommentWrap,
		"wrapLine":         WrapLine,
		"wrapField":        WrapField,
		"availabilityNote": AvailabilityNote,
		"needsSep":         needsSepIR,
		"qualify":          qualify,
	}).Parse(respTmplStr))

	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("rendering RespFragment for %s: %w", f.Op.Group, err)
	}
	return sb.String(), nil
}

func (f *RespFragment) renderMapResp() (string, error) {
	elemType := "json.RawMessage"
	if f.Op.RespElemType != nil {
		elemType = f.qualifyElemType(f.Op.RespElemType.Name)
	}

	data := struct {
		TypePrefix  string
		Description string
		DocsURL     string
		ElemType    string
	}{
		TypePrefix:  f.Op.TypePrefix,
		Description: f.Op.Description,
		DocsURL:     f.Op.DocsURL,
		ElemType:    elemType,
	}

	tmpl := template.Must(template.New("mapResp").Funcs(template.FuncMap{
		"comment": CommentWrap,
	}).Parse(mapRespTmplStr))

	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("rendering map RespFragment for %s: %w", f.Op.Group, err)
	}
	return sb.String(), nil
}

func (f *RespFragment) renderArrayResp() (string, error) {
	elemType := "json.RawMessage"
	if f.Op.RespElemType != nil {
		elemType = f.qualifyElemType(f.Op.RespElemType.Name)
	}

	data := struct {
		TypePrefix  string
		Description string
		DocsURL     string
		ElemType    string
	}{
		TypePrefix:  f.Op.TypePrefix,
		Description: f.Op.Description,
		DocsURL:     f.Op.DocsURL,
		ElemType:    elemType,
	}

	tmpl := template.Must(template.New("arrayResp").Funcs(template.FuncMap{
		"comment": CommentWrap,
	}).Parse(arrayRespTmplStr))

	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("rendering array RespFragment for %s: %w", f.Op.Group, err)
	}
	return sb.String(), nil
}

func (f *RespFragment) renderRawResp() (string, error) {
	data := struct {
		TypePrefix  string
		Description string
		DocsURL     string
	}{
		TypePrefix:  f.Op.TypePrefix,
		Description: f.Op.Description,
		DocsURL:     f.Op.DocsURL,
	}

	tmpl := template.Must(template.New("rawResp").Funcs(template.FuncMap{
		"comment": CommentWrap,
	}).Parse(rawRespTmplStr))

	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("rendering raw RespFragment for %s: %w", f.Op.Group, err)
	}
	return sb.String(), nil
}

func (f *RespFragment) qualifyElemType(name string) string {
	if !f.Op.IsPlugin || f.Registry == nil {
		return name
	}
	return qualifyType(name, f.Registry)
}

func (f *RespFragment) hasJSONRaw() bool {
	if f.Op.Response == nil {
		return false
	}
	for _, field := range f.Op.Response.Fields {
		if strings.Contains(field.GoType, "json.RawMessage") {
			return true
		}
	}
	for _, st := range f.Op.SiblingTypes {
		for _, field := range st.Fields {
			if strings.Contains(field.GoType, "json.RawMessage") {
				return true
			}
		}
	}
	return false
}

func (f *RespFragment) hasCrossPackageTypes() bool {
	if f.Registry == nil || f.Op.Response == nil {
		return false
	}
	for _, field := range f.Op.Response.Fields {
		if isCrossPackageType(field.GoType, f.Registry) {
			return true
		}
	}
	return false
}

const respTmplStr = `// {{.TypePrefix}}Resp represents the response for the {{.Group}} operation.
{{- if .Description}}
{{comment .Description}}
{{- end}}
{{- with availabilityNote .VersionAdded .VersionDeprecated .DeprecationMsg}}
{{wrapLine .}}
{{- end}}
{{- if .DocsURL}}
//
// See: {{.DocsURL}}
{{- end}}
type {{.TypePrefix}}Resp struct {
{{- range $i, $f := .RespFields}}
{{- if needsSep $.RespFields $i}}
{{end}}
{{- if $f.Comment}}
	{{wrapField $f.Comment}}
{{- end}}
{{- with availabilityNote $f.VersionAdded $f.VersionDeprecated $f.DeprecationMsg}}
{{- if $f.Comment}}
	//
{{- end}}
	{{wrapField .}}
{{- end}}
{{- if $f.IsEmbed}}
	{{qualify $f.GoType}}
{{- else}}
	{{$f.GoName}} {{qualify $f.GoType}} ` + "`" + `json:"{{$f.JSONName}}{{if $f.OmitEmpty}},omitempty{{end}}"` + "`" + `
{{- end}}
{{- end}}

	response *opensearch.Response
}

// Inspect returns the raw OpenSearch response for debugging or advanced use.
func (r {{.TypePrefix}}Resp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// RawBody returns a fresh reader over the original response bytes,
// useful when the typed response struct is incomplete for your use case.
func (r {{.TypePrefix}}Resp) RawBody() io.Reader {
	if r.response == nil || len(r.response.RawBody()) == 0 {
		return nil
	}
	return bytes.NewReader(r.response.RawBody())
}
`

const mapRespTmplStr = `// {{.TypePrefix}}Resp represents the response for the {{.TypePrefix}} operation.
// The response body is a JSON object keyed by resource name.
{{- if .Description}}
{{comment .Description}}
{{- end}}
{{- if .DocsURL}}
//
// See: {{.DocsURL}}
{{- end}}
type {{.TypePrefix}}Resp struct {
	Entries  map[string]{{.ElemType}} ` + "`" + `json:"-"` + "`" + `
	response *opensearch.Response
}

// UnmarshalJSON decodes the response body as a string-keyed map.
func (r *{{.TypePrefix}}Resp) UnmarshalJSON(b []byte) error {
	return json.Unmarshal(b, &r.Entries)
}

// MarshalJSON encodes the response body for comparison testing.
func (r {{.TypePrefix}}Resp) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.Entries)
}

// Inspect returns the raw OpenSearch response for debugging or advanced use.
func (r {{.TypePrefix}}Resp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// RawBody returns a fresh reader over the original response bytes,
// useful when the typed response struct is incomplete for your use case.
func (r {{.TypePrefix}}Resp) RawBody() io.Reader {
	if r.response == nil || len(r.response.RawBody()) == 0 {
		return nil
	}
	return bytes.NewReader(r.response.RawBody())
}
`

const arrayRespTmplStr = `// {{.TypePrefix}}Resp represents the response for the {{.TypePrefix}} operation.
// The response body is a JSON array of records.
{{- if .Description}}
{{comment .Description}}
{{- end}}
{{- if .DocsURL}}
//
// See: {{.DocsURL}}
{{- end}}
type {{.TypePrefix}}Resp struct {
	Records  []{{.ElemType}} ` + "`" + `json:"-"` + "`" + `
	response *opensearch.Response
}

// UnmarshalJSON decodes the response body as an array of records.
func (r *{{.TypePrefix}}Resp) UnmarshalJSON(b []byte) error {
	return json.Unmarshal(b, &r.Records)
}

// MarshalJSON encodes the response body for comparison testing.
func (r {{.TypePrefix}}Resp) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.Records)
}

// Inspect returns the raw OpenSearch response for debugging or advanced use.
func (r {{.TypePrefix}}Resp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// RawBody returns a fresh reader over the original response bytes,
// useful when the typed response struct is incomplete for your use case.
func (r {{.TypePrefix}}Resp) RawBody() io.Reader {
	if r.response == nil || len(r.response.RawBody()) == 0 {
		return nil
	}
	return bytes.NewReader(r.response.RawBody())
}
`

const rawRespTmplStr = `// {{.TypePrefix}}Resp represents the response for the {{.TypePrefix}} operation.
// The response body has a dynamic schema and is captured as raw JSON.
{{- if .Description}}
{{comment .Description}}
{{- end}}
{{- if .DocsURL}}
//
// See: {{.DocsURL}}
{{- end}}
type {{.TypePrefix}}Resp struct {
	Body     json.RawMessage ` + "`" + `json:"-"` + "`" + `
	response *opensearch.Response
}

// UnmarshalJSON captures the raw response body.
//
//nolint:unparam // error return required by json.Unmarshaler; raw passthrough never fails
func (r *{{.TypePrefix}}Resp) UnmarshalJSON(b []byte) error {
	r.Body = append(r.Body[:0], b...)
	return nil
}

// MarshalJSON returns the raw response body for comparison testing.
//
//nolint:unparam // error return required by json.Marshaler; raw passthrough never fails
func (r {{.TypePrefix}}Resp) MarshalJSON() ([]byte, error) {
	if r.Body == nil {
		return build.NullJSON, nil
	}
	return r.Body, nil
}

// Inspect returns the raw OpenSearch response for debugging or advanced use.
func (r {{.TypePrefix}}Resp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// RawBody returns a fresh reader over the original response bytes,
// useful when the typed response struct is incomplete for your use case.
func (r {{.TypePrefix}}Resp) RawBody() io.Reader {
	if r.response == nil || len(r.response.RawBody()) == 0 {
		return nil
	}
	return bytes.NewReader(r.response.RawBody())
}
`

// needsSepIR returns true if a blank line should precede field[i].
func needsSepIR(fields []ir.Field, i int) bool {
	if i == 0 {
		return false
	}
	return fieldHasAnnotationIR(fields[i-1]) || fieldHasAnnotationIR(fields[i])
}

func fieldHasAnnotationIR(f ir.Field) bool {
	return f.Comment != "" || f.VersionAdded != "" || f.VersionDeprecated != ""
}

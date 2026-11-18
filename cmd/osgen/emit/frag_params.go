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

	"github.com/opensearch-project/opensearch-go/cmd/osgen/v5/ir"
)

// ParamsFragment renders the Params struct and its get() method.
type ParamsFragment struct {
	Op       *ir.Operation
	Registry *ir.TypeRegistry
}

// Imports returns the imports the Params fragment needs.
func (f *ParamsFragment) Imports() []Import {
	imps := []Import{
		{Path: LocalModule + "/internal/params", Alias: "osparams"},
	}
	if f.Op.IsPlugin && f.Registry != nil {
		imps = append(imps, Import{Path: f.Registry.CoreImport})
	}
	for _, p := range f.Op.QueryParams {
		switch p.Kind {
		case ir.ParamDuration:
			imps = append(imps, Import{Path: "time"})
		case ir.ParamBool:
			imps = append(imps, Import{Path: "strconv"})
		case ir.ParamInt, ir.ParamFloat:
			imps = append(imps, Import{Path: "strconv"})
		case ir.ParamList:
			imps = append(imps, Import{Path: LocalModule + "/internal/apiutil"})
		case ir.ParamString:
			// no extra import
		}
	}
	return imps
}

// prefixFormatOverrides maps an operation-group prefix (the segment before
// the first dot in the operation Group) to the value the Go SDK emits for
// the `format` query param when the caller leaves it unset and no
// per-operation entry in groupFormatOverrides applies.
//
// The override exists because some endpoint families have a server-side
// default response format that the SDK cannot decode (cat/list default to
// text/plain), or that does not match the SDK's typed response struct.
// Each entry mirrors the corresponding `_common___<X>ResponseFormat`
// schema defined in the spec. Keys are sorted alphabetically; keep them
// that way when adding entries.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var prefixFormatOverrides = map[string]string{
	"cat":  "json",
	"list": "json",
	"ppl":  "jdbc",
	"sql":  "jdbc",
}

// groupFormatOverrides holds per-operation overrides consulted before
// prefixFormatOverrides. Used where the family default does not apply,
// for example explain endpoints whose typed response matches the `json`
// shape rather than the JDBC envelope used by query endpoints. Keys are
// sorted alphabetically; keep them that way when adding entries.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var groupFormatOverrides = map[string]string{
	"ppl.explain": "json",
	"sql.explain": "json",
}

// FormatOverride returns the SDK-side runtime default for op's `format` query
// param, or the empty string when no override applies. An operation gets one
// only when it declares `format` (the spec declares it per operation, so a
// family member such as cat help may not); the value comes from
// groupFormatOverrides (full group name) first, then from
// prefixFormatOverrides keyed by the segment of Group before the first dot.
func FormatOverride(op *ir.Operation) string {
	if !hasQueryParam(op, "format") {
		return ""
	}
	if v, ok := groupFormatOverrides[op.Group]; ok {
		return v
	}
	prefix, _, found := strings.Cut(op.Group, ".")
	if !found {
		return ""
	}
	return prefixFormatOverrides[prefix]
}

// hasQueryParam reports whether op declares the query parameter wireName.
func hasQueryParam(op *ir.Operation, wireName string) bool {
	for _, p := range op.QueryParams {
		if p.WireName == wireName {
			return true
		}
	}
	return false
}

// Body renders the Params struct (and its associated apply method) for the
// operation.
func (f *ParamsFragment) Body() (string, error) {
	corePkg := ""
	if f.Op.IsPlugin && f.Registry != nil {
		corePkg = f.Registry.CorePkg
	}
	qualify := func(name string) string {
		if corePkg != "" {
			return corePkg + "." + name
		}
		return name
	}
	qualifyGoType := qualifierFunc(f.Op.IsPlugin, f.Registry)

	tmpl := template.Must(template.New("params").Funcs(template.FuncMap{
		"wrapField":        WrapField,
		"availabilityNote": AvailabilityNote,
		"qualify":          qualify,
		"qualifyType":      qualifyGoType,
		"enumRef":          func(goType string) string { return qualifyGoType(strings.TrimPrefix(goType, "[]")) },
		"isDuration":       func(k ir.ParamKind) bool { return k == ir.ParamDuration },
		"isBool":           func(k ir.ParamKind) bool { return k == ir.ParamBool },
		"isList":           func(k ir.ParamKind) bool { return k == ir.ParamList },
		"isInt":            func(k ir.ParamKind) bool { return k == ir.ParamInt },
		"isIntPtr":         func(p ir.QueryParam) bool { return p.Kind == ir.ParamInt && p.GoType == "*int" },
		"isFloat":          func(k ir.ParamKind) bool { return k == ir.ParamFloat },
		"isFloatPtr":       func(p ir.QueryParam) bool { return p.Kind == ir.ParamFloat && p.GoType == "*float64" },
		"isStringEnum":     func(p ir.QueryParam) bool { return p.IsStringEnum },
		"formatOverride":   func() string { return FormatOverride(f.Op) },
	}).Parse(paramsTmplStr))

	var sb strings.Builder
	if err := tmpl.Execute(&sb, f.Op); err != nil {
		return "", fmt.Errorf("rendering ParamsFragment for %s: %w", f.Op.Group, err)
	}
	return sb.String(), nil
}

const paramsTmplStr = `// {{.TypePrefix}}Params represents query parameters for the {{.TypePrefix}}Req.
type {{.TypePrefix}}Params struct {
	{{qualify "TimeoutParams"}}
	{{qualify "DebugParams"}}
{{- range $i, $p := .QueryParams}}
{{- if $i}}
{{end}}
{{- if $p.Description}}
	{{wrapField $p.Description}}
{{- end}}
{{- with availabilityNote $p.VersionAdded $p.VersionDeprecated $p.DeprecationMsg}}
{{- if $p.Description}}
	//
{{- end}}
	{{wrapField .}}
{{- end}}
{{- if $p.Default}}
{{- if or $p.Description $p.Deprecated}}
	//
{{- end}}
	// Default: {{$p.Default}}.
{{- end}}
{{- if isStringEnum $p}}
{{- if or $p.Description $p.Default (availabilityNote $p.VersionAdded $p.VersionDeprecated $p.DeprecationMsg)}}
	//
{{- end}}
	// See [{{enumRef $p.GoType}}] for valid values.
{{- end}}
	{{$p.GoName}} {{qualifyType $p.GoType}}
{{- end}}
}

func (r {{.TypePrefix}}Params) get() map[string]string {
	var params map[string]string
	set := func(k, v string) {
		if params == nil {
			params = make(map[string]string)
		}
		params[k] = v
	}
	osparams.EncodeTimeout(r.TimeoutParams, set)
	osparams.EncodeDebug(r.DebugParams, set)
{{- with formatOverride}}
	if r.Format == "" {
		set("format", "{{.}}")
	}
{{- end}}
{{range .QueryParams}}
{{- if isDuration .Kind}}
	if r.{{.GoName}} != 0 {
		set("{{.WireName}}", formatDuration(r.{{.GoName}}))
	}
{{- else if isBool .Kind}}
	if r.{{.GoName}} != nil {
		set("{{.WireName}}", strconv.FormatBool(*r.{{.GoName}}))
	}
{{- else if isList .Kind}}
	if len(r.{{.GoName}}) > 0 {
		set("{{.WireName}}", apiutil.JoinStrings(r.{{.GoName}}, ","))
	}
{{- else if isIntPtr .}}
	if r.{{.GoName}} != nil {
		set("{{.WireName}}", strconv.Itoa(*r.{{.GoName}}))
	}
{{- else if isInt .Kind}}
	if r.{{.GoName}} != 0 {
		set("{{.WireName}}", strconv.Itoa(r.{{.GoName}}))
	}
{{- else if isFloatPtr .}}
	if r.{{.GoName}} != nil {
		set("{{.WireName}}", strconv.FormatFloat(*r.{{.GoName}}, 'f', -1, 64))
	}
{{- else if isFloat .Kind}}
	if r.{{.GoName}} != 0 {
		set("{{.WireName}}", strconv.FormatFloat(r.{{.GoName}}, 'f', -1, 64))
	}
{{- else if isStringEnum .}}
	if r.{{.GoName}} != "" {
		set("{{.WireName}}", string(r.{{.GoName}}))
	}
{{- else}}
	if r.{{.GoName}} != "" {
		set("{{.WireName}}", r.{{.GoName}})
	}
{{- end}}
{{end}}
	return params
}
`

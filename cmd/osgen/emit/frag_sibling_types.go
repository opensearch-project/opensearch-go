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

// SiblingTypesFragment renders operation-local struct types (non-union siblings).
type SiblingTypesFragment struct {
	Op       *ir.Operation
	Types    []*ir.Type
	Registry *ir.TypeRegistry
}

// Imports returns the imports the sibling-types fragment needs.
func (f *SiblingTypesFragment) Imports() []Import {
	var imps []Import
	hasJSON := false
	hasCrossPkg := false
	for _, t := range f.Types {
		for _, field := range t.Fields {
			if !hasJSON && strings.Contains(field.GoType, "json.RawMessage") {
				hasJSON = true
			}
			if !hasCrossPkg && f.Op.IsPlugin && f.Registry != nil && isCrossPackageType(field.GoType, f.Registry) {
				hasCrossPkg = true
			}
		}
	}
	if hasJSON {
		imps = append(imps, Import{Path: "encoding/json"})
	}
	if hasCrossPkg {
		imps = append(imps, Import{Path: f.Registry.CoreImport})
	}
	return imps
}

// Body renders the sibling type definitions co-located with an operation.
func (f *SiblingTypesFragment) Body() (string, error) {
	if len(f.Types) == 0 {
		return "", nil
	}

	qualify := qualifierFunc(f.Op.IsPlugin, f.Registry)
	data := struct {
		Group string
		Types []*ir.Type
	}{
		Group: f.Op.Group,
		Types: f.Types,
	}

	tmpl := template.Must(template.New("siblings").Funcs(template.FuncMap{
		"comment":          CommentWrap,
		"wrapField":        WrapField,
		"availabilityNote": AvailabilityNote,
		"needsSep":         needsSepIR,
		"qualify":          qualify,
	}).Parse(siblingTypesTmplStr))

	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("rendering SiblingTypesFragment for %s: %w", f.Op.Group, err)
	}
	return sb.String(), nil
}

const siblingTypesTmplStr = `{{range .Types}}
// {{.Name}} is a typed component of the {{$.Group}} operation.
{{- if .Comment}}
{{comment .Comment}}
{{- end}}
{{- $fields := .Fields}}
type {{.Name}} struct {
{{- range $i, $f := .Fields}}
{{- if needsSep $fields $i}}
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
}
{{end}}`

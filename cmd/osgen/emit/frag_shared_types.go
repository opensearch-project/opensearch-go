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

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

// SharedTypesFragment renders shared struct types (non-union) for types_gen.go.
type SharedTypesFragment struct {
	Types []*ir.Type
}

// Imports returns the imports the shared-types fragment needs.
func (f *SharedTypesFragment) Imports() []Import {
	for _, t := range f.Types {
		for _, field := range t.Fields {
			if strings.Contains(field.GoType, "json.RawMessage") {
				return []Import{{Path: "encoding/json"}}
			}
		}
	}
	return nil
}

// Body renders the shared type definitions emitted into the core package.
func (f *SharedTypesFragment) Body() (string, error) {
	if len(f.Types) == 0 {
		return "", nil
	}

	var sb strings.Builder
	if err := sharedTypesFmtTmpl.Execute(&sb, f.Types); err != nil {
		return "", fmt.Errorf("rendering SharedTypesFragment: %w", err)
	}
	return sb.String(), nil
}

//nolint:gochecknoglobals // const-ish read-only template
var sharedTypesFmtTmpl = template.Must(template.New("sharedTypes").Funcs(template.FuncMap{
	"comment":          CommentWrap,
	"wrapField":        WrapField,
	"availabilityNote": AvailabilityNote,
	"needsSep":         needsSepIR,
}).Parse(`{{range .}}
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
	{{$f.GoType}}
{{- else}}
	{{$f.GoName}} {{$f.GoType}} ` + "`" + `json:"{{$f.JSONName}}{{if $f.OmitEmpty}},omitempty{{end}}"` + "`" + `
{{- end}}
{{- end}}
}
{{end}}`))

// NewSharedTypesFile builds a Target for types_gen.go.
func NewSharedTypesFile(outDir, pkg string, types []*ir.Type) Target {
	var structTypes []*ir.Type
	for _, t := range types {
		if t.Kind == ir.TypeStruct && t.Scope == ir.ScopeShared {
			structTypes = append(structTypes, t)
		}
	}
	if len(structTypes) == 0 {
		return nil
	}
	return &File{
		FilePath:  outDir + "/types_gen.go",
		Package:   pkg,
		Fragments: []Fragment{&SharedTypesFragment{Types: structTypes}},
	}
}

// NewUnionTypesFile builds a Target for unions_gen.go.
func NewUnionTypesFile(outDir, pkg string, types []*ir.Type) Target {
	var unionTypes []*ir.Type
	for _, t := range types {
		if (t.Kind == ir.TypeUnion || t.Kind == ir.TypeLazyUnion) && t.Scope == ir.ScopeShared {
			unionTypes = append(unionTypes, t)
		}
	}
	if len(unionTypes) == 0 {
		return nil
	}
	return &File{
		FilePath:  outDir + "/unions_gen.go",
		Package:   pkg,
		Fragments: []Fragment{&UnionFragment{Types: unionTypes}},
	}
}

// NewEnumTypesFile builds a Target for enums_gen.go.
func NewEnumTypesFile(outDir, pkg string, types []*ir.Type) Target {
	var enumTypes []*ir.Type
	for _, t := range types {
		if t.Kind == ir.TypeEnum && t.Scope == ir.ScopeShared {
			enumTypes = append(enumTypes, t)
		}
	}
	if len(enumTypes) == 0 {
		return nil
	}
	return &File{
		FilePath:  outDir + "/enums_gen.go",
		Package:   pkg,
		Fragments: []Fragment{&EnumFragment{Types: enumTypes}},
	}
}

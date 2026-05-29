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

// UnionFragment renders discriminated union types (both strict and lazy).
// When Op is non-nil and represents a plugin operation, branch types are
// qualified with the core package prefix (e.g. opensearchapi.FieldSort)
// so the generated plugin file references shared types correctly.
type UnionFragment struct {
	Op       *ir.Operation
	Types    []*ir.Type
	Registry *ir.TypeRegistry
}

// Imports returns the imports the union-types fragment needs.
func (f *UnionFragment) Imports() []Import {
	if len(f.Types) == 0 {
		return nil
	}
	imps := []Import{
		{Path: "bytes"},
		{Path: "encoding/json"},
		{Path: "fmt"},
		{Path: LocalModule + "/internal/build"},
	}
	if f.Op != nil && f.Op.IsPlugin && f.Registry != nil && f.hasCrossPkgBranch() {
		imps = append(imps, Import{Path: f.Registry.CoreImport})
	}
	return imps
}

// hasCrossPkgBranch reports whether any union branch references a
// shared (core-package) type that needs cross-package qualification
// when this fragment is emitted into a plugin package.
func (f *UnionFragment) hasCrossPkgBranch() bool {
	for _, t := range f.Types {
		for _, b := range t.Branches {
			if isCrossPackageType(b.GoType, f.Registry) {
				return true
			}
		}
	}
	return false
}

// Body renders the union type definitions (and their UnmarshalJSON methods).
func (f *UnionFragment) Body() (string, error) {
	if len(f.Types) == 0 {
		return "", nil
	}

	var qualify func(string) string
	if f.Op != nil {
		qualify = qualifierFunc(f.Op.IsPlugin, f.Registry)
	} else {
		qualify = func(s string) string { return s }
	}

	var sb strings.Builder
	tmpl := template.Must(template.New("union").Funcs(template.FuncMap{
		"comment":   CommentWrap,
		"constName": unionConstNameIR,
		"tokenStr":  tokenClassStr,
		"isTryEach": func(k ir.TypeKind) bool { return k == ir.TypeLazyUnion },
		"qualify":   qualify,
	}).Parse(unionFragTmplText))

	if err := tmpl.Execute(&sb, f.Types); err != nil {
		return "", fmt.Errorf("rendering UnionFragment: %w", err)
	}
	return sb.String(), nil
}

func tokenClassStr(tc ir.TokenClass) string {
	switch tc {
	case ir.TokenObject:
		return "object"
	case ir.TokenArray:
		return "array"
	case ir.TokenString:
		return "string"
	case ir.TokenNumber:
		return "number"
	case ir.TokenBool:
		return "bool"
	default:
		return "unknown"
	}
}

func unionConstNameIR(unionName, branchName string) string {
	return unionName + branchName + "Type"
}

//nolint:gochecknoglobals // const-ish read-only template body
const unionFragTmplText = `{{- range $t := .}}
{{- if isTryEach $t.Kind}}
{{- if $t.Comment}}
{{comment $t.Comment}}
{{- else}}
// {{$t.Name}} is a discriminated union type (try-each, newest version first).
{{- end}}
// Use Type() to determine which branch was decoded, then call
// the corresponding accessor.
type {{$t.Name}} struct {
	typ   {{$t.Name}}Type
	raw   json.RawMessage
	value any
}

// {{$t.Name}}Type discriminates the branches of {{$t.Name}}.
type {{$t.Name}}Type int

const (
	{{$t.Name}}UnknownType {{$t.Name}}Type = iota
{{- range $t.Branches}}
	{{constName $t.Name .Name}}
{{- end}}
)

// Type returns which union branch was populated during decoding.
// Returns {{$t.Name}}UnknownType if the value has not been decoded.
func (u *{{$t.Name}}) Type() {{$t.Name}}Type { return u.typ }

// RawJSON returns the original JSON bytes for escape-hatch decoding.
func (u *{{$t.Name}}) RawJSON() json.RawMessage { return u.raw }

// SetRaw stages pre-encoded JSON for marshaling. MarshalJSON emits raw
// verbatim when no typed branch is set. Use the New{{$t.Name}}From*
// constructors to populate a typed branch instead; SetRaw is the typed
// escape hatch for callers that already have wire-format bytes.
func (u *{{$t.Name}}) SetRaw(raw json.RawMessage) {
	u.raw = raw
	u.value = nil
	u.typ = {{$t.Name}}UnknownType
}
{{range $t.Branches}}
// {{.Name}} returns the {{qualify .GoType}} branch value.
func (u *{{$t.Name}}) {{.Name}}() {{qualify .GoType}} {
	v, _ := u.value.({{qualify .GoType}})
	return v
}

// New{{$t.Name}}From{{.Name}} returns a {{$t.Name}} populated with v
// on the {{.Name}} branch.
func New{{$t.Name}}From{{.Name}}(v {{qualify .GoType}}) {{$t.Name}} {
	return {{$t.Name}}{
		typ:   {{constName $t.Name .Name}},
		value: v,
	}
}
{{end}}
func (u *{{$t.Name}}) UnmarshalJSON(data []byte) error {
	u.raw = append(u.raw[:0], data...)
	if len(data) == 0 || bytes.Equal(data, build.NullJSON) {
		return nil
	}
{{- range $t.Branches}}
	{
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err == nil {
			u.typ = {{constName $t.Name .Name}}
			u.value = v
			return nil
		}
	}
{{- end}}
	return fmt.Errorf("{{$t.Name}}: no branch matched JSON: %s", data[:min(len(data), 64)])
}

func (u {{$t.Name}}) MarshalJSON() ([]byte, error) {
	if u.value != nil {
		return json.Marshal(u.value)
	}
	if len(u.raw) > 0 {
		return u.raw, nil
	}
	return build.NullJSON, nil
}
{{- else}}
{{- if $t.Comment}}
{{comment $t.Comment}}
{{- else}}
// {{$t.Name}} is a discriminated union type.
{{- end}}
// Use Type() to determine which branch was decoded, then call
// the corresponding accessor.
type {{$t.Name}} struct {
	typ   {{$t.Name}}Type
	raw   json.RawMessage
	value any
}

// {{$t.Name}}Type discriminates the branches of {{$t.Name}}.
type {{$t.Name}}Type int

const (
	{{$t.Name}}UnknownType {{$t.Name}}Type = iota
{{- range $t.Branches}}
	{{constName $t.Name .Name}}
{{- end}}
)

// Type returns which union branch was populated during decoding.
// Returns {{$t.Name}}UnknownType if the value has not been decoded.
func (u *{{$t.Name}}) Type() {{$t.Name}}Type { return u.typ }

// RawJSON returns the original JSON bytes for escape-hatch decoding.
func (u *{{$t.Name}}) RawJSON() json.RawMessage { return u.raw }

// SetRaw stages pre-encoded JSON for marshaling. MarshalJSON emits raw
// verbatim when no typed branch is set. Use the New{{$t.Name}}From*
// constructors to populate a typed branch instead; SetRaw is the typed
// escape hatch for callers that already have wire-format bytes.
func (u *{{$t.Name}}) SetRaw(raw json.RawMessage) {
	u.raw = raw
	u.value = nil
	u.typ = {{$t.Name}}UnknownType
}
{{range $t.Branches}}
// {{.Name}} returns the {{qualify .GoType}} branch value.
func (u *{{$t.Name}}) {{.Name}}() {{qualify .GoType}} {
	v, _ := u.value.({{qualify .GoType}})
	return v
}

// New{{$t.Name}}From{{.Name}} returns a {{$t.Name}} populated with v
// on the {{.Name}} branch.
func New{{$t.Name}}From{{.Name}}(v {{qualify .GoType}}) {{$t.Name}} {
	return {{$t.Name}}{
		typ:   {{constName $t.Name .Name}},
		value: v,
	}
}
{{end}}
func (u *{{$t.Name}}) UnmarshalJSON(data []byte) error {
	u.raw = append(u.raw[:0], data...)
	if len(data) == 0 || bytes.Equal(data, build.NullJSON) {
		return nil
	}
	switch {
{{- range $t.Branches}}
{{- if eq (tokenStr .TokenClass) "object"}}
	case data[0] == '{':
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = v
{{- else if eq (tokenStr .TokenClass) "array"}}
	case data[0] == '[':
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = v
{{- else if eq (tokenStr .TokenClass) "string"}}
	case data[0] == '"':
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = v
{{- else if eq (tokenStr .TokenClass) "number"}}
	case data[0] >= '0' && data[0] <= '9' || data[0] == '-':
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = v
{{- else if eq (tokenStr .TokenClass) "bool"}}
	case data[0] == 't' || data[0] == 'f':
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = v
{{- end}}
{{- end}}
	default:
		return fmt.Errorf("{{$t.Name}}: unexpected JSON token: %s", data[:1])
	}
	return nil
}

func (u {{$t.Name}}) MarshalJSON() ([]byte, error) {
	if u.value != nil {
		return json.Marshal(u.value)
	}
	if len(u.raw) > 0 {
		return u.raw, nil
	}
	return build.NullJSON, nil
}
{{- end}}
{{end}}`

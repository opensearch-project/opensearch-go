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
type UnionFragment struct {
	Types []*ir.Type
}

func (f *UnionFragment) Imports() []Import {
	if len(f.Types) == 0 {
		return nil
	}
	return []Import{
		{Path: "bytes"},
		{Path: "encoding/json"},
		{Path: "fmt"},
		{Path: LocalModule + "/internal/build"},
	}
}

func (f *UnionFragment) Body() (string, error) {
	if len(f.Types) == 0 {
		return "", nil
	}

	var sb strings.Builder
	if err := unionFragTmpl.Execute(&sb, f.Types); err != nil {
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

var unionFragTmpl = template.Must(template.New("union").Funcs(template.FuncMap{
	"comment":   CommentWrap,
	"constName": unionConstNameIR,
	"tokenStr":  tokenClassStr,
	"isTryEach": func(k ir.TypeKind) bool { return k == ir.TypeLazyUnion },
}).Parse(`{{- range $t := .}}
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
{{range $t.Branches}}
// {{.Name}} returns the {{.GoType}} branch value.
func (u *{{$t.Name}}) {{.Name}}() {{.GoType}} {
	v, _ := u.value.({{.GoType}})
	return v
}
{{end}}
func (u *{{$t.Name}}) UnmarshalJSON(data []byte) error {
	u.raw = append(u.raw[:0], data...)
	if len(data) == 0 || bytes.Equal(data, build.NullJSON) {
		return nil
	}
{{- range $t.Branches}}
	{
		var v {{.GoType}}
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
{{range $t.Branches}}
// {{.Name}} returns the {{.GoType}} branch value.
func (u *{{$t.Name}}) {{.Name}}() {{.GoType}} {
	v, _ := u.value.({{.GoType}})
	return v
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
		var v {{.GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = v
{{- else if eq (tokenStr .TokenClass) "array"}}
	case data[0] == '[':
		var v {{.GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = v
{{- else if eq (tokenStr .TokenClass) "string"}}
	case data[0] == '"':
		var v {{.GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = v
{{- else if eq (tokenStr .TokenClass) "number"}}
	case data[0] >= '0' && data[0] <= '9' || data[0] == '-':
		var v {{.GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = v
{{- else if eq (tokenStr .TokenClass) "bool"}}
	case data[0] == 't' || data[0] == 'f':
		var v {{.GoType}}
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
{{end}}`))

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"fmt"
	"go/format"
	"strings"
	"text/template"
)

var unionTmpl = template.Must(template.New("union").Funcs(template.FuncMap{
	"lcFirst":      lcFirst,
	"hasToken":     unionHasTokenClass,
	"needsTryEach": unionNeedsTryEach,
	"comment":      commentWrap,
	"constName":    unionConstName,
}).Parse(`
{{- range $t := .Types}}
{{- if $t.IsLazy}}
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
	if len(data) == 0 || string(data) == "null" {
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
	return []byte("null"), nil
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
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	switch {
{{- range $t.Branches}}
{{- if eq .TokenClass "object"}}
	case data[0] == '{':
		var v {{.GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = v
{{- else if eq .TokenClass "array"}}
	case data[0] == '[':
		var v {{.GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = v
{{- else if eq .TokenClass "string"}}
	case data[0] == '"':
		var v {{.GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = v
{{- else if eq .TokenClass "number"}}
	case data[0] >= '0' && data[0] <= '9' || data[0] == '-':
		var v {{.GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = v
{{- else if eq .TokenClass "bool"}}
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
	return []byte("null"), nil
}
{{- end}}
{{end}}`))

// unionHasTokenClass returns true if any branch uses the given token class.
func unionHasTokenClass(branches []unionBranch, class string) bool {
	for _, b := range branches {
		if b.TokenClass == class {
			return true
		}
	}
	return false
}

// unionConstName builds the const identifier for a union branch.
// Always concatenates unionName + branchName + "Type" so two branches
// that would otherwise collide via de-stutter (e.g. union="Foo" with
// branches "Bar" and "FooBar" both rendering as "FooBarType") instead
// produce distinct identifiers ("FooBarType" vs "FooFooBarType").
// The result may stutter when the spec author already prefixed the
// branch with the union name; uniqueness wins over elegance here.
func unionConstName(unionName, branchName string) string {
	return unionName + branchName + "Type"
}

// unionNeedsTryEach returns true if any two branches share the same token class,
// meaning byte-prefix discrimination is insufficient for at least one pair.
func unionNeedsTryEach(branches []unionBranch) bool {
	if len(branches) < 2 {
		return false
	}
	seen := make(map[string]bool, len(branches))
	for _, b := range branches {
		if seen[b.TokenClass] {
			return true
		}
		seen[b.TokenClass] = true
	}
	return false
}

// renderUnionTypesFile generates Go source for all union types in the list.
func renderUnionTypesFile(types []*goType, pkg string) (string, error) {
	var unions []*goType
	for _, t := range types {
		if t.IsUnion {
			unions = append(unions, t)
		}
	}
	if len(unions) == 0 {
		return "", nil
	}

	var sb strings.Builder
	sb.WriteString("// Code generated by cmd/osgen; DO NOT EDIT.\n\n")
	sb.WriteString("package " + pkg + "\n\n")
	sb.WriteString("import (\n")
	sb.WriteString("\t\"encoding/json\"\n")
	sb.WriteString("\t\"fmt\"\n")
	sb.WriteString(")\n")

	data := struct {
		Types []*goType
	}{Types: unions}

	if err := unionTmpl.Execute(&sb, data); err != nil {
		return "", err
	}

	formatted, err := format.Source([]byte(sb.String()))
	if err != nil {
		return "", fmt.Errorf("gofmt union types: %w\n%s", err, sb.String())
	}
	return string(formatted), nil
}

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

// UnionFragment renders discriminated union types (both strict and lazy).
// When Op is non-nil and represents a plugin operation, branch types are
// qualified with the core package prefix (e.g. opensearchapi.FieldSort)
// so the generated plugin file references shared types correctly.
type UnionFragment struct {
	Op       *ir.Operation
	Types    []*ir.Type
	Registry *ir.TypeRegistry
}

// Imports returns the imports the union-types fragment needs. fmt is only used
// by the try-each/first-byte variants (fmt.Errorf); bytes only by the variants
// that null-check with bytes.Equal (everything except the lazy-accessor one).
// build.HasJSONKeys is only emitted for try-each unions.
func (f *UnionFragment) Imports() []Import {
	if len(f.Types) == 0 {
		return nil
	}
	imps := []Import{
		{Path: "encoding/json"},
		{Path: LocalModule + "/internal/build"},
	}
	var needBytes, needFmt bool
	for _, t := range f.Types {
		switch {
		case t.Merge != nil:
			needBytes = true
		case t.LazyAccessors:
			// json + build only
		case t.Kind == ir.TypeLazyUnion: // try-each
			needBytes, needFmt = true, true
		default: // first-byte switch
			needBytes, needFmt = true, true
		}
	}
	if needBytes {
		imps = append(imps, Import{Path: "bytes"})
	}
	if needFmt {
		imps = append(imps, Import{Path: "fmt"})
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
		"comment":    CommentWrap,
		"constName":  unionConstNameIR,
		"tokenStr":   tokenClassStr,
		"isTryEach":  func(k ir.TypeKind) bool { return k == ir.TypeLazyUnion },
		"qualify":    qualify,
		"quotedKeys": quotedKeys,
		"embedField": embedFieldName,
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

// embedFieldName returns the selector used to reference an embedded type: the
// substring after the last package qualifier dot. "opensearchapi.GetResult"
// -> "GetResult"; "GetResult" -> "GetResult".
func embedFieldName(goType string) string {
	if i := strings.LastIndex(goType, "."); i >= 0 {
		return goType[i+1:]
	}
	return goType
}

// quotedKeys renders a slice of field names as a comma-separated list of
// Go double-quoted string literals, for splicing into a build.HasJSONKeys
// call in the generated try-each discriminator.
func quotedKeys(keys []string) string {
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = fmt.Sprintf("%q", k)
	}
	return strings.Join(quoted, ", ")
}

const unionFragTmplText = `{{- range $t := .}}
{{- if $t.Merge}}
{{- if $t.Comment}}
{{comment $t.Comment}}
{{- else}}
// {{$t.Name}} is a discriminated union type (single-pass merge decode).
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

// RawJSON returns the union's JSON bytes. After decoding these are borrowed
// from the response buffer: valid only while the owning response value is
// reachable, must not be mutated, and must be copied if retained beyond it.
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
	if v, ok := u.value.(*{{qualify .GoType}}); ok {
		return *v
	}
	var zero {{qualify .GoType}}
	return zero
}

// New{{$t.Name}}From{{.Name}} returns a {{$t.Name}} populated with v
// on the {{.Name}} branch.
func New{{$t.Name}}From{{.Name}}(v {{qualify .GoType}}) {{$t.Name}} {
	return {{$t.Name}}{
		typ:   {{constName $t.Name .Name}},
		value: &v,
	}
}
{{end}}
func (u *{{$t.Name}}) UnmarshalJSON(data []byte) error {
	u.raw = data
	u.value = nil
	u.typ = {{$t.Name}}UnknownType
	if len(data) == 0 || bytes.Equal(data, build.NullJSON) {
		return nil
	}
	// Single decode: embed the permissive (primary) branch and probe for the
	// discriminating keys of the other branches in one pass. encoding/json
	// populates the embedded primary directly; the probes only test presence.
	type merged struct {
		{{qualify $t.Merge.PrimaryGoType}}
	{{- range $t.Merge.Probes}}
		{{.GoName}} json.RawMessage ` + "`json:\"{{.JSONKey}}\"`" + `
	{{- end}}
	}
	var m merged
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
{{- range $t.Merge.Branches}}
	if {{range $i, $p := .PresentProbes}}{{if $i}} && {{end}}len(m.{{$p}}) > 0{{end}} {
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{.Const}}
		u.value = &v
		return nil
	}
{{- end}}
	u.typ = {{$t.Merge.PrimaryConst}}
	u.value = &m.{{embedField (qualify $t.Merge.PrimaryGoType)}}
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
{{- else if $t.LazyAccessors}}
{{- if $t.Comment}}
{{comment $t.Comment}}
{{- else}}
// {{$t.Name}} is a discriminated union with no wire discriminator.
{{- end}}
// Its branches are indistinguishable from the response bytes alone (the type
// is determined by the request), so the raw JSON is retained and decoded on
// demand by the As<Branch>() accessors. There is deliberately no Type() method
// or discriminant constants: the wire never identifies the branch.
type {{$t.Name}} struct {
	raw   json.RawMessage
	value any
}

// RawJSON returns the union's JSON bytes. After decoding these are borrowed
// from the response buffer: valid only while the owning response value is
// reachable, must not be mutated, and must be copied if retained beyond it.
func (u *{{$t.Name}}) RawJSON() json.RawMessage { return u.raw }

// SetRaw stages pre-encoded JSON for marshaling.
func (u *{{$t.Name}}) SetRaw(raw json.RawMessage) {
	u.raw = raw
	u.value = nil
}
{{range $t.Branches}}
// As{{.Name}} decodes the union as {{qualify .GoType}}. The caller selects the
// type it requested; an empty value and nil error mean the union is empty.
func (u *{{$t.Name}}) As{{.Name}}() ({{qualify .GoType}}, error) {
	if v, ok := u.value.(*{{qualify .GoType}}); ok {
		return *v, nil
	}
	var v {{qualify .GoType}}
	if len(u.raw) == 0 {
		return v, nil
	}
	err := json.Unmarshal(u.raw, &v)
	return v, err
}

// New{{$t.Name}}From{{.Name}} returns a {{$t.Name}} populated with v
// on the {{.Name}} branch.
func New{{$t.Name}}From{{.Name}}(v {{qualify .GoType}}) {{$t.Name}} {
	return {{$t.Name}}{
		value: &v,
	}
}
{{end}}
func (u *{{$t.Name}}) UnmarshalJSON(data []byte) error {
	u.raw = data
	u.value = nil
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
{{- else if isTryEach $t.Kind}}
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

// RawJSON returns the union's JSON bytes. After decoding these are borrowed
// from the response buffer: valid only while the owning response value is
// reachable, must not be mutated, and must be copied if retained beyond it.
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
	if v, ok := u.value.(*{{qualify .GoType}}); ok {
		return *v
	}
	var zero {{qualify .GoType}}
	return zero
}

// New{{$t.Name}}From{{.Name}} returns a {{$t.Name}} populated with v
// on the {{.Name}} branch.
func New{{$t.Name}}From{{.Name}}(v {{qualify .GoType}}) {{$t.Name}} {
	return {{$t.Name}}{
		typ:   {{constName $t.Name .Name}},
		value: &v,
	}
}
{{end}}
func (u *{{$t.Name}}) UnmarshalJSON(data []byte) error {
	u.raw = data
	u.value = nil
	u.typ = {{$t.Name}}UnknownType
	if len(data) == 0 || bytes.Equal(data, build.NullJSON) {
		return nil
	}
	// Pass 1: branches that declare required (discriminator) fields. A branch
	// is eligible only when the payload carries every required key, so a more
	// specific branch (e.g. an error sub-response keyed by "error") is not
	// absorbed by a structurally permissive success branch. encoding/json does
	// not enforce a schema's "required" set, hence the explicit key probe.
{{- range $t.Branches}}
{{- if .Required}}
	if build.HasJSONKeys(data, {{quotedKeys .Required}}) {
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err == nil {
			u.typ = {{constName $t.Name .Name}}
			u.value = &v
			return nil
		}
	}
{{- end}}
{{- end}}
	// Pass 2: permissive branches with no required fields, tried newest-first.
{{- range $t.Branches}}
{{- if not .Required}}
	{
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err == nil {
			u.typ = {{constName $t.Name .Name}}
			u.value = &v
			return nil
		}
	}
{{- end}}
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

// RawJSON returns the union's JSON bytes. After decoding these are borrowed
// from the response buffer: valid only while the owning response value is
// reachable, must not be mutated, and must be copied if retained beyond it.
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
	if v, ok := u.value.(*{{qualify .GoType}}); ok {
		return *v
	}
	var zero {{qualify .GoType}}
	return zero
}

// New{{$t.Name}}From{{.Name}} returns a {{$t.Name}} populated with v
// on the {{.Name}} branch.
func New{{$t.Name}}From{{.Name}}(v {{qualify .GoType}}) {{$t.Name}} {
	return {{$t.Name}}{
		typ:   {{constName $t.Name .Name}},
		value: &v,
	}
}
{{end}}
func (u *{{$t.Name}}) UnmarshalJSON(data []byte) error {
	u.raw = data
	u.value = nil
	u.typ = {{$t.Name}}UnknownType
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
		u.value = &v
{{- else if eq (tokenStr .TokenClass) "array"}}
	case data[0] == '[':
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = &v
{{- else if eq (tokenStr .TokenClass) "string"}}
	case data[0] == '"':
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = &v
{{- else if eq (tokenStr .TokenClass) "number"}}
	case data[0] >= '0' && data[0] <= '9' || data[0] == '-':
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = &v
{{- else if eq (tokenStr .TokenClass) "bool"}}
	case data[0] == 't' || data[0] == 'f':
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = &v
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

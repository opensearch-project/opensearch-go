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

// EnumFragment renders int-backed (const iota) enum types for a closed set of
// wire values. Each enum emits: an int type with a zero-value <Name>Unknown
// sentinel and one const per value; name<->value lookup maps; String();
// MarshalJSON (const -> wire name); and UnmarshalJSON, which maps a known wire
// name to its const and, on an unknown value, sets the receiver to
// <Name>Unknown and returns a typed *Unknown<Name>Error carrying the raw value.
// Returning the error aborts the enclosing response decode (closed-set
// enforcement); callers recover the raw value via errors.As.
type EnumFragment struct {
	Types []*ir.Type
}

// Imports returns the imports the enum fragment needs: encoding/json for
// Marshal/UnmarshalJSON and fmt for the error type's Error method.
func (f *EnumFragment) Imports() []Import {
	if len(f.Types) == 0 {
		return nil
	}
	return []Import{
		{Path: "encoding/json"},
		{Path: "fmt"},
	}
}

// Body renders the enum type definitions. Const identifiers are precomputed in
// the IR bridge (acronym-aware), so the template only substitutes prepared
// names and values.
func (f *EnumFragment) Body() (string, error) {
	if len(f.Types) == 0 {
		return "", nil
	}

	var sb strings.Builder
	if err := enumFragTmpl.Execute(&sb, f.Types); err != nil {
		return "", fmt.Errorf("rendering EnumFragment: %w", err)
	}
	return sb.String(), nil
}

//nolint:gochecknoglobals // const-ish read-only template
var enumFragTmpl = template.Must(template.New("enum").Funcs(template.FuncMap{
	"comment":  CommentWrap,
	"unexport": lowerFirst,
}).Parse(`{{range $t := .}}
{{- $names := printf "%sNames" (unexport $t.Name)}}
{{- $values := printf "%sValues" (unexport $t.Name)}}
{{- if $t.Comment}}
{{comment $t.Comment}}
{{- end}}
type {{$t.Name}} int

const (
	{{$t.Name}}Unknown {{$t.Name}} = iota
{{- range $m := $t.EnumMembers}}
	{{$m.ConstName}}
{{- end}}
)

//nolint:gochecknoglobals // generated read-only enum lookup table
var {{$names}} = map[{{$t.Name}}]string{
{{- range $m := $t.EnumMembers}}
	{{$m.ConstName}}: "{{$m.Value}}",
{{- end}}
}

//nolint:gochecknoglobals // generated read-only enum lookup table
var {{$values}} = map[string]{{$t.Name}}{
{{- range $m := $t.EnumMembers}}
	"{{$m.Value}}": {{$m.ConstName}},
{{- end}}
}

// String returns the wire name of s, or "" for {{$t.Name}}Unknown.
func (s {{$t.Name}}) String() string {
	return {{$names}}[s]
}

// MarshalJSON encodes s as its wire name. It errors on an unknown const value.
func (s {{$t.Name}}) MarshalJSON() ([]byte, error) {
	name, ok := {{$names}}[s]
	if !ok {
		return nil, fmt.Errorf("invalid {{$t.Name}} value %d", int(s))
	}
	return json.Marshal(name)
}

// UnmarshalJSON decodes a wire name into its const. An unrecognized value sets
// s to {{$t.Name}}Unknown and returns an *Unknown{{$t.Name}}Error carrying the
// raw value; callers recover it via errors.As.
func (s *{{$t.Name}}) UnmarshalJSON(data []byte) error {
	var v string
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	code, ok := {{$values}}[v]
	if !ok {
		*s = {{$t.Name}}Unknown
		return &Unknown{{$t.Name}}Error{Value: v}
	}
	*s = code
	return nil
}

// Unknown{{$t.Name}}Error is returned when decoding a {{$t.Name}} wire value
// that is not in the known set (e.g. a status from a newer server). Value holds
// the raw wire string.
type Unknown{{$t.Name}}Error struct {
	Value string
}

func (e *Unknown{{$t.Name}}Error) Error() string {
	return fmt.Sprintf("unknown {{$t.Name}} %q", e.Value)
}
{{end}}`))

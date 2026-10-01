// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import (
	"fmt"
	"path"
	"strings"
	"text/template"

	"github.com/opensearch-project/opensearch-go/cmd/osgen/v5/ir"
)

// OpaqueStringFragment renders opaque token types (x-type-name): a struct
// around an unexported string, so callers cannot build one from a string
// literal or treat it as a string by accident. Each type gets Parse<Name>,
// which rejects an empty string; String and IsSet; and MarshalText and
// UnmarshalText, which encoding/json uses, so the wire form is the bare string.
// MarshalText rejects an empty value too, so a request can't send one.
type OpaqueStringFragment struct {
	Types []*ir.Type
}

// Imports returns the imports the opaque fragment needs: errors for the
// Parse error.
func (f *OpaqueStringFragment) Imports() []Import {
	if len(f.Types) == 0 {
		return nil
	}
	return []Import{{Path: "errors"}}
}

// Body renders the opaque type definitions.
func (f *OpaqueStringFragment) Body() (string, error) {
	if len(f.Types) == 0 {
		return "", nil
	}

	var sb strings.Builder
	if err := opaqueStringFragTmpl.Execute(&sb, f.Types); err != nil {
		return "", fmt.Errorf("rendering OpaqueStringFragment: %w", err)
	}
	return sb.String(), nil
}

//nolint:gochecknoglobals // const-ish read-only template
var opaqueStringFragTmpl = template.Must(template.New("opaqueString").Funcs(template.FuncMap{
	"comment": CommentWrap,
	"pkgName": path.Base,
}).Parse(`{{range $t := .}}
{{- if $t.Comment}}
{{comment $t.Comment}}
//
{{- end}}
// Build one with Parse{{$t.Name}}; read it with String. It encodes as the bare
// string on the wire.
type {{$t.Name}} struct {
	s string
}

// errEmpty{{$t.Name}} is returned for an empty {{$t.Name}}, both when parsing one
// and when one would be sent.
//
//nolint:gochecknoglobals // generated read-only sentinel
var errEmpty{{$t.Name}} = errors.New("{{pkgName $t.Package}}: empty {{$t.Name}}")

// Parse{{$t.Name}} returns s as a {{$t.Name}}. The value is opaque, so the only
// check is that s is not empty.
func Parse{{$t.Name}}(s string) ({{$t.Name}}, error) {
	if s == "" {
		return {{$t.Name}}{}, errEmpty{{$t.Name}}
	}
	return {{$t.Name}}{s: s}, nil
}

// String returns v as sent on the wire.
func (v {{$t.Name}}) String() string { return v.s }

// IsSet reports whether v holds a value, rather than being the zero {{$t.Name}}.
func (v {{$t.Name}}) IsSet() bool { return v.s != "" }

// MarshalText returns v as sent on the wire. It fails for an empty {{$t.Name}},
// so a request can't carry one by accident; decoding stays lenient, so a
// response that omits the value still decodes, with IsSet false.
func (v {{$t.Name}}) MarshalText() ([]byte, error) {
	if v.s == "" {
		return nil, errEmpty{{$t.Name}}
	}
	return []byte(v.s), nil
}

// UnmarshalText sets v from its wire form.
func (v *{{$t.Name}}) UnmarshalText(text []byte) error {
	v.s = string(text)
	return nil
}
{{end}}`))

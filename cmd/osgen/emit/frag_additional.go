// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import (
	"strconv"
	"strings"

	"github.com/opensearch-project/opensearch-go/cmd/osgen/v5/ir"
)

// additionalData is the input of the "additional" template.
type additionalData struct {
	TypeName string
	*ir.AdditionalProperties
	// Cases is DeclaredKeys quoted for a switch case list.
	Cases string
}

func newAdditionalData(typeName string, a *ir.AdditionalProperties) additionalData {
	cases := make([]string, len(a.DeclaredKeys))
	for i, k := range a.DeclaredKeys {
		cases[i] = strconv.Quote(k)
	}
	return additionalData{TypeName: typeName, AdditionalProperties: a, Cases: strings.Join(cases, ", ")}
}

// additionalTmplStr renders the MarshalJSON and UnmarshalJSON of a struct that
// keeps its additionalProperties in a map field. MarshalJSON splices the map's
// members into the object after the declared fields, and refuses a key that
// names a declared field, which would write the member twice. UnmarshalJSON
// decodes the declared fields as usual and every other member into the map.
const additionalTmplStr = `{{define "additional"}}
// MarshalJSON writes the {{.GoName}} entries as members of the object, after
// the declared fields.
func (t {{.TypeName}}) MarshalJSON() ([]byte, error) {
	type declared {{.TypeName}}
	b, err := json.Marshal(declared(t))
	if err != nil || len(t.{{.GoName}}) == 0 {
		return b, err
	}
	for k := range t.{{.GoName}} {
		switch k {
		case {{.Cases}}:
			return nil, fmt.Errorf("{{.TypeName}}: {{.GoName}} key %q is a declared field", k)
		}
	}
	extra, err := json.Marshal(t.{{.GoName}})
	if err != nil {
		return nil, err
	}
	if len(b) == len("{}") {
		return extra, nil
	}
	return append(append(b[:len(b)-1], ','), extra[1:]...), nil
}

// UnmarshalJSON decodes the declared fields, and every other member into
// {{.GoName}}.
func (t *{{.TypeName}}) UnmarshalJSON(data []byte) error {
	type declared {{.TypeName}}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return err
	}
	if err := json.Unmarshal(data, (*declared)(t)); err != nil {
		return err
	}
	t.{{.GoName}} = nil
	for k, v := range members {
		switch k {
		case {{.Cases}}:
			continue
		}
		var val {{.ValueType}}
		if err := json.Unmarshal(v, &val); err != nil {
			return fmt.Errorf("{{.TypeName}}: {{.GoName}}[%q]: %w", k, err)
		}
		if t.{{.GoName}} == nil {
			t.{{.GoName}} = make(map[string]{{.ValueType}}, len(members))
		}
		t.{{.GoName}}[k] = val
	}
	return nil
}
{{- end}}`

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/opensearch-project/opensearch-go/cmd/osgen/v5/ir"
)

// extraKeysField renders the map field a struct carries for keys the spec leaves
// open next to its fixed properties. The `json:"-"` tag keeps the codec from
// emitting it as a nested object; the generated methods flatten it instead.
func extraKeysField(t *ir.Type, qualify func(string) string) string {
	x := t.ExtraKeys
	doc := "holds the keys the spec leaves open next to the fixed properties."
	if x.KeyTitle != "" {
		doc = fmt.Sprintf("maps each %s name to its value, next to the fixed properties.", x.KeyTitle)
	}
	if x.RawValue {
		doc += "\n\t// A decoded value re-encodes as the bytes it was decoded from; to change one," +
			"\n\t// replace the entry with a value built by a constructor."
	}
	return fmt.Sprintf("// %s %s\n\t%s map[string]%s `json:\"-\"`", x.GoName, doc, x.GoName, qualify(x.ValueType))
}

// extraKeysMethods renders MarshalJSON and UnmarshalJSON for a struct with an
// extra-keys map. Fixed properties go through an alias type so the methods do
// not recurse and embedded types keep their own handling; every other key
// belongs to the map.
func extraKeysMethods(t *ir.Type, qualify func(string) string) string {
	x := t.ExtraKeys
	valType := qualify(x.ValueType)

	quoted := make([]string, len(x.Known))
	for i, k := range x.Known {
		quoted[i] = strconv.Quote(k)
	}

	// A union value type has RawJSON and SetRaw, which keep a value the spec's
	// types do not describe as raw JSON.
	var rawDoc string
	encodeVal := `b, err := json.Marshal(val)
		if err != nil {
			return nil, err
		}`
	decodeVal := `if err := json.Unmarshal(r, &val); err != nil {
			return err
		}`
	if x.RawValue {
		encodeVal = `b := []byte(val.RawJSON())
		if len(b) == 0 {
			var err error
			if b, err = json.Marshal(val); err != nil {
				return nil, err
			}
		}`
		decodeVal = `if err := json.Unmarshal(r, &val); err != nil {
			val = ` + valType + `{}
			val.SetRaw(append(json.RawMessage(nil), r...))
		}`
		rawDoc = "\n// A value the spec's types do not describe is kept as raw JSON rather than failing the" +
			"\n// whole decode, so a query decodes and re-encodes unchanged."
	}

	return fmt.Sprintf(`
// isFixedKey reports whether k names a fixed property. The codec matches property
// names case-insensitively, so a key it puts in a fixed field never goes in the map.
func (%[1]s) isFixedKey(k string) bool {
	for _, fixed := range [...]string{%[4]s} {
		if strings.EqualFold(k, fixed) {
			return true
		}
	}
	return false
}

// MarshalJSON writes the fixed properties and the %[3]s map as one object.
func (v %[1]s) MarshalJSON() ([]byte, error) {
	type %[1]sFixed %[1]s
	fixed, err := json.Marshal(%[1]sFixed(v))
	if err != nil {
		return nil, err
	}
	if len(v.%[3]s) == 0 {
		return fixed, nil
	}
	out := make(map[string]json.RawMessage, len(v.%[3]s))
	if err := json.Unmarshal(fixed, &out); err != nil {
		return nil, err
	}
	for k, val := range v.%[3]s {
		if v.isFixedKey(k) {
			continue
		}
		%[5]s
		out[k] = b
	}
	return json.Marshal(out)
}

// UnmarshalJSON reads the fixed properties, then puts every other key in %[3]s.%[7]s
func (v *%[1]s) UnmarshalJSON(data []byte) error {
	type %[1]sFixed %[1]s
	if err := json.Unmarshal(data, (*%[1]sFixed)(v)); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	v.%[3]s = nil
	for k, r := range raw {
		if v.isFixedKey(k) {
			continue
		}
		var val %[2]s
		%[6]s
		if v.%[3]s == nil {
			v.%[3]s = make(map[string]%[2]s)
		}
		v.%[3]s[k] = val
	}
	return nil
}
`, t.Name, valType, x.GoName, strings.Join(quoted, ", "), encodeVal, decodeVal, rawDoc)
}

// hasExtraKeys reports whether any type in ts carries an extra-keys map.
func hasExtraKeys(ts []*ir.Type) bool {
	for _, t := range ts {
		if t.ExtraKeys != nil {
			return true
		}
	}
	return false
}

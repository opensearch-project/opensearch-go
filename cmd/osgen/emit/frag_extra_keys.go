// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import (
	"fmt"
	"maps"
	"slices"
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

// extraKeysMethods renders MarshalJSON and UnmarshalJSON for a struct that
// declares an extra-keys map or embeds a type that does; x is the resolved
// description (see extraKeysPlan). Fixed properties go through a wrapper struct
// whose MarshalJSON and UnmarshalJSON fields hide the methods an embedded type
// promotes, so the methods neither recurse nor hand the whole object to an
// embedded type's methods; every other key belongs to the map.
func extraKeysMethods(t *ir.Type, x *ir.ExtraKeys, qualify func(string) string) string {
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
			"\n// whole decode, so the object decodes and re-encodes unchanged."
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
	type %[1]sFixed struct {
		%[1]s
		MarshalJSON   struct{} `+"`json:\"-\"`"+`
		UnmarshalJSON struct{} `+"`json:\"-\"`"+`
	}
	fixed, err := json.Marshal(%[1]sFixed{%[1]s: v})
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
			return nil, fmt.Errorf("%[1]s: key %%q in %[3]s names a fixed property", k)
		}
		%[5]s
		out[k] = b
	}
	return json.Marshal(out)
}

// UnmarshalJSON reads the fixed properties, then puts every other key in %[3]s.%[7]s
func (v *%[1]s) UnmarshalJSON(data []byte) error {
	type %[1]sFixed struct {
		%[1]s
		MarshalJSON   struct{} `+"`json:\"-\"`"+`
		UnmarshalJSON struct{} `+"`json:\"-\"`"+`
	}
	var p %[1]sFixed
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*v = p.%[1]s
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

// maxEmbedDepth bounds an embed chain. A chain this deep means the IR holds an
// embed cycle.
const maxEmbedDepth = 16

// extraKeysPlan maps each type in ts that declares or embeds (transitively) a type
// with an extra-keys map to the description its methods are rendered from. The map
// itself lives on the declaring type; an embedder promotes it. An embedder's known
// keys are the JSON names of its whole embed chain, because the promoted methods
// would otherwise see only the embedded part. Embedded types resolve by name among
// ts; once a chain reaches an extra-keys type, an embed that does not resolve
// panics, as a truncated key list would collect a named field into the map.
func extraKeysPlan(ts []*ir.Type) map[*ir.Type]*ir.ExtraKeys {
	byName := make(map[string]*ir.Type, len(ts))
	for _, t := range ts {
		byName[t.Name] = t
	}
	plan := make(map[*ir.Type]*ir.ExtraKeys)
	for _, t := range ts {
		if t.ExtraKeys != nil {
			plan[t] = t.ExtraKeys
			continue
		}
		var base *ir.ExtraKeys
		known := make(map[string]struct{})
		unresolved, unresolvedIn := "", ""
		var walk func(cur *ir.Type, depth int)
		walk = func(cur *ir.Type, depth int) {
			if depth > maxEmbedDepth {
				panic(fmt.Sprintf("extraKeysPlan: embed chain of %s exceeds %d levels at %s (embed cycle?)", t.Name, maxEmbedDepth, cur.Name))
			}
			for _, f := range cur.Fields {
				if !f.IsEmbed {
					known[f.JSONName] = struct{}{}
					continue
				}
				emb, ok := byName[embedFieldName(f.GoType)]
				if !ok {
					unresolved, unresolvedIn = f.GoType, cur.Name
					continue
				}
				if emb.ExtraKeys != nil && base == nil {
					base = emb.ExtraKeys
				}
				walk(emb, depth+1)
			}
		}
		walk(t, 0)
		if base == nil {
			continue
		}
		if unresolved != "" {
			panic(fmt.Sprintf("extraKeysPlan: %s embeds %s via %s, which is not among the fragment's types", t.Name, unresolved, unresolvedIn))
		}
		x := *base
		x.Known = slices.Sorted(maps.Keys(known))
		plan[t] = &x
	}
	return plan
}

// hasExtraKeys reports whether any type in ts gets extra-keys methods.
func hasExtraKeys(ts []*ir.Type) bool {
	return len(extraKeysPlan(ts)) > 0
}

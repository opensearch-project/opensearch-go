// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package clientmodel

import (
	"encoding/json"
	"testing"
)

// vendoredModel is the model built from the vendored spec (make gen-client-model).
const vendoredModel = "../../../opensearch-client-model.json"

func load(t *testing.T) *Model {
	t.Helper()
	m, err := Load(vendoredModel)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParseRejectsOtherFormatsAndVersions(t *testing.T) {
	for _, doc := range []string{
		`{"format":"other","version":"0.2.0","types":{}}`,
		`{"format":"opensearch-client-model","version":"0.1.0","types":{}}`,
	} {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("Parse(%s) succeeded, want an error", doc)
		}
	}
}

func TestOrderedKeepsKeyOrder(t *testing.T) {
	var o Ordered[int]
	if err := json.Unmarshal([]byte(`{"z":1,"a":2,"m":3}`), &o); err != nil {
		t.Fatal(err)
	}
	if got := o.Keys; len(got) != 3 || got[0] != "z" || got[1] != "a" || got[2] != "m" {
		t.Errorf("keys = %v, want [z a m]", got)
	}
	out, err := json.Marshal(o)
	if err != nil || string(out) != `{"z":1,"a":2,"m":3}` {
		t.Errorf("Marshal = %s, %v", out, err)
	}
}

func TestVendoredModel(t *testing.T) {
	m := load(t)

	lookup := m.Types["_common.query_dsl___TermsLookup"]
	if lookup == nil || lookup.TypeKind != KindObject {
		t.Fatalf("TermsLookup = %+v, want an object", lookup)
	}
	for _, name := range []string{"index", "path"} {
		if f, ok := lookup.Fields.Get(name); !ok || !f.Required {
			t.Errorf("TermsLookup.%s = %+v, want a required field", name, f)
		}
	}

	property := m.Types["_common.mapping___Property"]
	if property == nil || property.UnionKind != "tagged" || property.Discriminator != "type" || property.DefaultVariant != "object" {
		t.Fatalf("Property = %+v, want a tagged union on type with default object", property)
	}
	if v, ok := property.Variants.Get("text"); !ok || v.Ref != "_common.mapping___TextProperty" {
		t.Errorf("Property variant text = %+v", v)
	}

	container := m.Types["_common.aggregations___AggregationContainer"]
	if container == nil || len(container.Constraints) != 1 || container.Constraints[0].UnionKind != "container" {
		t.Fatalf("AggregationContainer = %+v, want one container constraint", container)
	}
	variants := container.Constraints[0].Variants
	if variants.Len() < 60 {
		t.Errorf("AggregationContainer has %d variants, want at least 60", variants.Len())
	}
	terms, _ := variants.Get("terms")
	if _, ok := terms.ExtraFields.Get("aggs"); !ok {
		t.Errorf("terms variant extra fields = %v, want aggs", terms.ExtraFields.Keys)
	}
	avg, _ := variants.Get("avg")
	if avg.ExtraFields.Len() != 0 {
		t.Errorf("avg variant extra fields = %v, want none", avg.ExtraFields.Keys)
	}

	if doc := m.Types["_common___TDocument"]; doc == nil || !doc.GenericParameter {
		t.Errorf("TDocument = %+v, want a generic parameter", doc)
	}
	if body := m.Responses["search___200"]; body == nil || body.MediaType != "application/json" {
		t.Errorf("search___200 = %+v", body)
	}
}

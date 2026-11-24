// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"sort"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/opensearch-project/opensearch-go/cmd/osgen/v5/clientmodel"
)

// TestClientModelAgreesOnTaggedUnions checks the client model against osgen's
// own reading of the vendored spec: every union osgen decodes by discriminator
// is a tagged union in the model, with the same property, default and tag
// value per branch, in branch order. It is the first step of moving the
// walker's type input to the model (see clientmodel/README.md).
func TestClientModelAgreesOnTaggedUnions(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromFile("../../opensearch-openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	model, err := clientmodel.LoadSpec("../../opensearch-openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(spec.Components.Schemas))
	for name := range spec.Components.Schemas {
		names = append(names, name)
	}
	sort.Strings(names)

	checked := 0
	for _, name := range names {
		schema := spec.Components.Schemas[name].Value
		if schema == nil {
			continue
		}
		branches := schema.OneOf
		if len(branches) == 0 {
			branches = schema.AnyOf
		}
		disc, values, ok := discriminatorValues(schema, branches)
		if !ok {
			continue
		}
		checked++
		entry := model.Types[name]
		if entry == nil || entry.UnionKind != "tagged" {
			t.Errorf("%s: osgen reads a discriminated union, the model has %+v", name, entry)
			continue
		}
		if entry.Discriminator != disc.PropertyName || entry.DefaultVariant != disc.DefaultValue {
			t.Errorf("%s: discriminator %q default %q, model %q default %q",
				name, disc.PropertyName, disc.DefaultValue, entry.Discriminator, entry.DefaultVariant)
		}
		if entry.Variants.Len() != len(values) {
			t.Errorf("%s: %d tag values, model has %d variants", name, len(values), entry.Variants.Len())
			continue
		}
		for i, tag := range entry.Variants.Keys {
			if values[i] != tag {
				t.Errorf("%s: branch %d tag %q, model %q", name, i, values[i], tag)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no discriminated unions in the vendored spec")
	}
	t.Logf("%d discriminated unions agree", checked)
}

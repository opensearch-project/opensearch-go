// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package clientmodel_test

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/opensearch-project/opensearch-go/cmd/osgen/v5/clientmodel"
)

const schemaRefPrefix = "#/components/schemas/"

const lowerModel = `{
  "format": "opensearch-client-model", "version": "0.2.0",
  "types": {
    "Base": {"type_kind": "object", "fields": {
      "id": {"type": {"scalar": "string"}, "required": true}}},
    "Doc": {"type_kind": "object", "bases": ["Base"], "description": "A doc.", "fields": {
      "id": {"type": {"scalar": "string"}, "required": true, "declared_in": "Base"},
      "zeta": {"type": {"scalar": "integer", "format": "int64"}, "required": true, "versions": {"added": "2.1"}},
      "alpha": {"type": {"ref": "Unit"}, "required": true, "description": "Alpha."}}},
    "Same": {"type_kind": "object", "bases": ["Base"], "fields": {
      "id": {"type": {"scalar": "string"}, "required": true, "description": "Redeclared."}}},
    "Unit": {"type_kind": "alias", "type": {"scalar": "integer", "format": "int64"}, "description": "A unit."},
    "UnitAlias": {"type_kind": "alias", "type": {"ref": "Unit"}},
    "Container": {"type_kind": "object", "fields": {"meta": {"type": {"any": true}, "required": false}},
      "constraints": [{"union_kind": "container", "variants": {
        "avg": {"value": {"ref": "Doc"}, "extra_fields": {}, "extra_constraints": []},
        "terms": {"value": {"ref": "Doc"},
          "extra_fields": {"aggs": {"type": {"map": {"ref": "Container"}}, "required": false}},
          "extra_constraints": []}}}]},
    "Role": {"type_kind": "union", "union_kind": "scalar", "exclusive": true,
      "members": [{"enum": ["data"]}, {"enum": ["master"]}],
      "branch_versions": [null, {"deprecated": "2.0", "deprecation_message": "Use cluster_manager."}]},
    "Shape": {"type_kind": "union", "union_kind": "tagged", "exclusive": true, "discriminator": "type", "default_variant": "doc",
      "variants": {"doc": {"ref": "Doc"}, "base": {"ref": "Base"}}, "titles": ["d", null]},
    "TDoc": {"type_kind": "alias", "type": {"any": true}, "generic_parameter": true}
  },
  "responses": {"get___200": {"media_type": "application/json", "type": {"ref": "Doc"}}},
  "request_bodies": {}
}`

func lowered(t *testing.T) *openapi3.T {
	t.Helper()
	m, err := clientmodel.Parse([]byte(lowerModel))
	if err != nil {
		t.Fatal(err)
	}
	spec := &openapi3.T{Components: &openapi3.Components{
		Schemas: openapi3.Schemas{
			"UnitAlias": {Ref: schemaRefPrefix + "Unit"},
			"Unit":      {Value: &openapi3.Schema{Extensions: map[string]any{"x-type-name": "Unit"}}},
		},
		Responses: openapi3.ResponseBodies{"get___200": {Value: &openapi3.Response{
			Content: openapi3.Content{"application/json": {Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{}}}},
		}}},
	}}
	if err := clientmodel.Lower(spec, m); err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestLowerObjectWithBases(t *testing.T) {
	doc := lowered(t).Components.Schemas["Doc"].Value
	if doc.Description != "A doc." || len(doc.AllOf) != 2 || doc.AllOf[0].Ref != schemaRefPrefix+"Base" {
		t.Fatalf("Doc = %+v, want allOf [$ref Base, own fields]", doc)
	}
	own := doc.AllOf[1].Value
	if _, inherited := own.Properties["id"]; inherited {
		t.Error("inherited field id is redeclared")
	}
	if len(own.Required) != 2 || own.Required[0] != "alpha" || own.Required[1] != "zeta" {
		t.Errorf("required = %v, want sorted [alpha zeta]", own.Required)
	}
	if v := own.Properties["zeta"].Extensions["x-version-added"]; v != "2.1" {
		t.Errorf("zeta x-version-added = %v", v)
	}
	alpha := own.Properties["alpha"]
	if alpha.Ref != schemaRefPrefix+"Unit" || alpha.Value.Description != "Alpha." {
		t.Errorf("alpha = %s %q, want $ref Unit described Alpha.", alpha.Ref, alpha.Value.Description)
	}
	if alpha.Value.Extensions["x-type-name"] != "Unit" {
		t.Error("alpha lost Unit's x-type-name")
	}
}

func TestLowerIdenticalRedeclarationIsInherited(t *testing.T) {
	same := lowered(t).Components.Schemas["Same"].Value
	if len(same.AllOf) != 1 || same.AllOf[0].Ref != schemaRefPrefix+"Base" {
		t.Errorf("Same = %+v, want only allOf [$ref Base]", same)
	}
}

func TestLowerBareRefAliasIsItsTarget(t *testing.T) {
	spec := lowered(t)
	alias := spec.Components.Schemas["UnitAlias"]
	if alias.Ref != schemaRefPrefix+"Unit" || alias.Value != spec.Components.Schemas["Unit"].Value {
		t.Errorf("UnitAlias = %+v, want a $ref sharing Unit's schema", alias)
	}
}

func TestLowerContainer(t *testing.T) {
	c := lowered(t).Components.Schemas["Container"].Value
	if len(c.AllOf) != 2 {
		t.Fatalf("Container allOf has %d members, want own fields and the container union", len(c.AllOf))
	}
	u := c.AllOf[1].Value
	if len(u.OneOf) != 2 {
		t.Fatalf("container union has %d branches", len(u.OneOf))
	}
	terms := u.OneOf[1].Value
	if terms.Required[0] != "terms" || terms.Properties["aggs"] == nil {
		t.Errorf("terms branch = %+v, want terms required and aggs allowed", terms)
	}
}

func TestLowerUnions(t *testing.T) {
	spec := lowered(t)
	role := spec.Components.Schemas["Role"].Value
	if role.OneOf[0].Value.Const != "data" {
		t.Errorf("a single-value enum branch lowers to const, got %+v", role.OneOf[0].Value)
	}
	if role.OneOf[1].Value.Extensions["x-version-deprecated"] != "2.0" {
		t.Error("branch versions not lowered")
	}
	shape := spec.Components.Schemas["Shape"].Value
	if shape.Discriminator == nil || shape.Discriminator.PropertyName != "type" || shape.Discriminator.Extensions["x-default"] != "doc" {
		t.Errorf("discriminator = %+v", shape.Discriminator)
	}
	if shape.OneOf[0].Ref != schemaRefPrefix+"Doc" || shape.OneOf[0].Value.Title != "d" {
		t.Errorf("first branch = %s %q, want Doc titled d", shape.OneOf[0].Ref, shape.OneOf[0].Value.Title)
	}
	if spec.Components.Schemas["TDoc"].Value.Extensions["x-is-generic-type-parameter"] != true {
		t.Error("generic parameter not marked")
	}
}

func TestLowerResponseBody(t *testing.T) {
	body := lowered(t).Components.Responses["get___200"].Value.Content["application/json"].Schema
	if body.Ref != schemaRefPrefix+"Doc" {
		t.Errorf("get___200 body = %+v, want $ref Doc", body)
	}
}

func TestLowerVendoredSpec(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromFile("../../../opensearch-openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m := load(t)
	if err := clientmodel.Lower(spec, m); err != nil {
		t.Fatal(err)
	}
	for name := range m.Types {
		if spec.Components.Schemas[name] == nil || spec.Components.Schemas[name].Value == nil {
			t.Errorf("%s not lowered", name)
		}
	}
}

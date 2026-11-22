// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"github.com/getkin/kin-openapi/openapi3"
)

// additionalPropertiesSchemas lists the object schemas whose additionalProperties
// osgen keeps next to their declared properties. Each of them names the member
// after a field, so without the map the request cannot be built:
// `{"terms": {"status": ["a", "b"], "boost": 1}}` has nowhere to put "status".
//
// The other schemas that declare both are left as they are. The generated
// MarshalJSON would be promoted into every type that embeds the schema, which
// then marshals only the embedded fields (MultiBucketBase is embedded by every
// bucket type), and the extra decoding pass costs response types such as
// SearchHit on every hit.
//
//nolint:gochecknoglobals // const-ish read-only table
var additionalPropertiesSchemas = newSet(
	"_common___GeoDistanceSort",
	"_common.query_dsl___TermsQuery",
)

// fallbackAdditionalGoName names the map field when the additionalProperties
// schema has no title, or its title is already a declared field's Go name.
const fallbackAdditionalGoName = "AdditionalProperties"

// additionalProperties returns the map field that carries the undeclared
// members of an object schema listed in additionalPropertiesSchemas, or nil.
// The field is named after the additionalProperties title (`terms` becomes
// Terms) and is tagged `json:"-"`: the type's MarshalJSON and UnmarshalJSON
// move its entries in and out of the object.
func (w *walker) additionalProperties(schema *openapi3.Schema, key, group string, fields []goField) *goAdditional {
	ap := schema.AdditionalProperties.Schema
	if ap == nil || !additionalPropertiesSchemas.has(key) || len(fields) == 0 {
		return nil
	}

	taken := make(set[string], len(fields))
	declared := make([]string, 0, len(fields))
	for _, f := range fields {
		if f.IsEmbed {
			// An embedded type's JSON names are not known here, so its members
			// would land in the map as well.
			return nil
		}
		taken.add(f.GoName)
		declared = append(declared, f.JSONName)
	}

	name := fallbackAdditionalGoName
	if ap.Value != nil && ap.Value.Title != "" {
		if n := baseGoName(ap.Value.Title); n != "" && !taken.has(n) {
			name = n
		}
	}

	comment := name + " holds the members not declared above, keyed by member name."
	if ap.Value != nil && ap.Value.Description != "" {
		comment = ap.Value.Description
	}

	return &goAdditional{
		GoName:       name,
		ValueType:    w.walkSchema(ap, key+"Value", group, false),
		Comment:      comment,
		DeclaredKeys: declared,
	}
}

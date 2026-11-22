// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

func TestAdditionalProperties(t *testing.T) {
	t.Parallel()

	const listed = "_common.query_dsl___TermsQuery"
	values := func(title string) openapi3.AdditionalProperties {
		return openapi3.AdditionalProperties{Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{
			Title: title,
			Type:  &openapi3.Types{openapi3.TypeArray},
			Items: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
		}}}
	}
	boost := goField{GoName: "Boost", JSONName: "boost"}
	name := goField{GoName: "Name", JSONName: "_name"}

	tests := []struct {
		name   string
		key    string
		schema *openapi3.Schema
		fields []goField
		want   *goAdditional
	}{
		{
			name:   "named after the title",
			key:    listed,
			schema: &openapi3.Schema{AdditionalProperties: values("terms")},
			fields: []goField{name, boost},
			want: &goAdditional{
				GoName:       "Terms",
				ValueType:    "[]string",
				Comment:      "Terms holds the members not declared above, keyed by member name.",
				DeclaredKeys: []string{"_name", "boost"},
			},
		},
		{
			name:   "title taken by a declared field",
			key:    listed,
			schema: &openapi3.Schema{AdditionalProperties: values("boost")},
			fields: []goField{boost},
			want: &goAdditional{
				GoName:       "AdditionalProperties",
				ValueType:    "[]string",
				Comment:      "AdditionalProperties holds the members not declared above, keyed by member name.",
				DeclaredKeys: []string{"boost"},
			},
		},
		{
			name:   "schema not listed",
			key:    "_common___Other",
			schema: &openapi3.Schema{AdditionalProperties: values("terms")},
			fields: []goField{boost},
		},
		{
			name:   "no additionalProperties schema",
			key:    listed,
			schema: &openapi3.Schema{},
			fields: []goField{boost},
		},
		{
			name:   "embedded field",
			key:    listed,
			schema: &openapi3.Schema{AdditionalProperties: values("terms")},
			fields: []goField{boost, {GoType: "QueryBase", IsEmbed: true}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := &walker{registry: newTypeRegistry(opensearchAPIPkgName), spec: &openapi3.T{}, inFlight: make(map[string]struct{})}
			require.Equal(t, tt.want, w.additionalProperties(tt.schema, tt.key, "test", tt.fields))
		})
	}
}

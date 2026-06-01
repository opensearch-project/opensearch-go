// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"slices"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

// newClassifySpec builds a minimal *ir.Spec whose registry and Types slice both
// reference the same type pointers, mirroring how convertToIR wires shared
// types before classifyUnions runs.
func newClassifySpec(types ...*ir.Type) *ir.Spec {
	spec := &ir.Spec{Registry: ir.NewTypeRegistry("opensearchapi", "x/opensearchapi")}
	for _, t := range types {
		if t.SchemaRef == "" {
			t.SchemaRef = t.Name // unique key so the registry does not dedup
		}
		spec.Types = append(spec.Types, t)
		spec.Registry.Register(t)
	}
	return spec
}

func structType(name string, fields ...ir.Field) *ir.Type {
	return &ir.Type{Name: name, Kind: ir.TypeStruct, Fields: fields}
}

func field(goName, jsonName, goType string) ir.Field {
	return ir.Field{GoName: goName, JSONName: jsonName, GoType: goType}
}

func TestClassifyUnions(t *testing.T) {
	tests := []struct {
		name string
		// setup returns the spec to classify and the union under test.
		setup func() (*ir.Spec, *ir.Type)

		wantMerge    bool
		wantLazy     bool
		wantPrimary  string   // expected embedded primary GoType (when wantMerge)
		wantProbes   []string // expected probe JSON keys (when wantMerge)
		wantBranches []string // expected discriminated branch GoTypes (when wantMerge)
	}{
		{
			name: "success|error wrapper merges, discriminated by the error key",
			setup: func() (*ir.Spec, *ir.Type) {
				success := structType("GetResult",
					field("ID", "_id", "string"),
					field("Index", "_index", "string"),
					field("Source", "_source", "json.RawMessage"),
				)
				errBranch := structType("MultiGetError",
					field("ID", "_id", "string"),
					field("Index", "_index", "string"),
					field("Error", "error", "ErrorCause"),
				)
				union := &ir.Type{Name: "DocsItem", Kind: ir.TypeLazyUnion, Branches: []ir.UnionBranch{
					{Name: "GetResult", GoType: "GetResult", TokenClass: ir.TokenObject},
					{Name: "MultiGetError", GoType: "MultiGetError", TokenClass: ir.TokenObject, Required: []string{"_id", "_index", "error"}},
				}}
				return newClassifySpec(success, errBranch, union), union
			},
			wantMerge:    true,
			wantPrimary:  "GetResult", // _id/_index shared, only "error" distinguishes
			wantProbes:   []string{"error"},
			wantBranches: []string{"MultiGetError"},
		},
		{
			name: "all-discriminated but mutually distinguishable merges (non-error primary)",
			setup: func() (*ir.Spec, *ir.Type) {
				status := structType("ScrollStatus",
					field("Batches", "batches", "int64"),
					field("Total", "total", "int64"),
				)
				errCause := structType("ErrorCause",
					field("Type", "type", "string"),
					field("Reason", "reason", "string"),
				)
				union := &ir.Type{Name: "StatusOrException", Kind: ir.TypeLazyUnion, Branches: []ir.UnionBranch{
					{Name: "Status", GoType: "ScrollStatus", TokenClass: ir.TokenObject, Required: []string{"batches", "total"}},
					{Name: "ErrorCause", GoType: "ErrorCause", TokenClass: ir.TokenObject, Required: []string{"type"}},
				}}
				return newClassifySpec(status, errCause, union), union
			},
			wantMerge:    true,
			wantPrimary:  "ScrollStatus", // non-error branch preferred as primary
			wantProbes:   []string{"type"},
			wantBranches: []string{"ErrorCause"},
		},
		{
			name: "caller-keyed all-permissive union -> lazy As<T>()",
			setup: func() (*ir.Spec, *ir.Type) {
				avg := structType("AvgAgg", field("Value", "value", "float64"))
				sum := structType("SumAgg", field("Value", "value", "float64"))
				union := &ir.Type{Name: "AggValue", Kind: ir.TypeLazyUnion, Branches: []ir.UnionBranch{
					{Name: "Avg", GoType: "AvgAgg", TokenClass: ir.TokenObject},
					{Name: "Sum", GoType: "SumAgg", TokenClass: ir.TokenObject},
				}}
				resp := structType("SearchResult", field("Aggregations", "aggregations", "map[string]AggValue"))
				return newClassifySpec(avg, sum, union, resp), union
			},
			wantLazy: true,
		},
		{
			name: "non-map all-permissive union left on try-each",
			setup: func() (*ir.Spec, *ir.Type) {
				a := structType("ShapeA", field("X", "x", "int"))
				b := structType("ShapeB", field("Y", "y", "int"))
				union := &ir.Type{Name: "DirectBody", Kind: ir.TypeLazyUnion, Branches: []ir.UnionBranch{
					{Name: "ShapeA", GoType: "ShapeA", TokenClass: ir.TokenObject},
					{Name: "ShapeB", GoType: "ShapeB", TokenClass: ir.TokenObject},
				}}
				resp := structType("Resp", field("Body", "body", "DirectBody"))
				return newClassifySpec(a, b, union, resp), union
			},
			// caller does not pick the type, so neither merge nor As<T>() applies
		},
		{
			name: "unembeddable (map) primary cannot merge",
			setup: func() (*ir.Spec, *ir.Type) {
				errBranch := structType("Err", field("Error", "error", "ErrorCause"))
				union := &ir.Type{Name: "OpenItem", Kind: ir.TypeLazyUnion, Branches: []ir.UnionBranch{
					{Name: "Map", GoType: "map[string]json.RawMessage", TokenClass: ir.TokenObject},
					{Name: "Err", GoType: "Err", TokenClass: ir.TokenObject, Required: []string{"error"}},
				}}
				return newClassifySpec(errBranch, union), union
			},
			// Map branch is permissive but not embeddable; Err shares "error"
			// with nothing usable -> no valid primary.
		},
		{
			name: "caller-keyed branches sharing a required key stay lazy (aggregation-like)",
			setup: func() (*ir.Spec, *ir.Type) {
				avg := structType("AvgAgg", field("Value", "value", "float64"))
				sum := structType("SumAgg", field("Value", "value", "float64"))
				// Both branches require "value" (as allOf flattening produces for
				// single-metric aggregates): not mutually distinguishable, so the
				// disjointness guard rejects the merge and Case B keeps As<T>().
				union := &ir.Type{Name: "MetricAgg", Kind: ir.TypeLazyUnion, Branches: []ir.UnionBranch{
					{Name: "Avg", GoType: "AvgAgg", TokenClass: ir.TokenObject, Required: []string{"value"}},
					{Name: "Sum", GoType: "SumAgg", TokenClass: ir.TokenObject, Required: []string{"value"}},
				}}
				resp := structType("SearchResult2", field("Aggregations", "aggregations", "map[string]MetricAgg"))
				return newClassifySpec(avg, sum, union, resp), union
			},
			wantLazy: true,
		},
		{
			name: "non-map branches sharing a required key are left on try-each",
			setup: func() (*ir.Spec, *ir.Type) {
				a := structType("VariantA", field("Type", "type", "string"), field("A", "a", "int"))
				b := structType("VariantB", field("Type", "type", "string"), field("B", "b", "int"))
				union := &ir.Type{Name: "TypeTagged", Kind: ir.TypeLazyUnion, Branches: []ir.UnionBranch{
					{Name: "VariantA", GoType: "VariantA", TokenClass: ir.TokenObject, Required: []string{"type"}},
					{Name: "VariantB", GoType: "VariantB", TokenClass: ir.TokenObject, Required: []string{"type"}},
				}}
				resp := structType("Body", field("V", "v", "TypeTagged"))
				return newClassifySpec(a, b, union, resp), union
			},
			// shared "type" -> not mergeable; not caller-keyed -> neither merge nor lazy
		},
		{
			name: "non-object branch skips classification entirely",
			setup: func() (*ir.Spec, *ir.Type) {
				obj := structType("Obj", field("X", "x", "int"))
				union := &ir.Type{Name: "ObjectOrString", Kind: ir.TypeLazyUnion, Branches: []ir.UnionBranch{
					{Name: "Obj", GoType: "Obj", TokenClass: ir.TokenObject},
					{Name: "Str", GoType: "string", TokenClass: ir.TokenString},
				}}
				return newClassifySpec(obj, union), union
			},
			// allObjectBranches false -> classifyUnions skips: no merge, no lazy.
		},
		{
			name: "permissive primary plus discriminated branch indistinguishable from it warns",
			setup: func() (*ir.Spec, *ir.Type) {
				// Primary is permissive (no required keys) and already declares
				// "status" in its fields, so the discriminated branch's only
				// required key is shared with the primary -> distinguishing set
				// is empty -> tryPrimary returns nil for every candidate ->
				// warn path fires.
				primary := structType("AckPrimary",
					field("Acknowledged", "acknowledged", "bool"),
					field("Status", "status", "string"),
				)
				disc := structType("AckDisc",
					field("Status", "status", "string"),
				)
				union := &ir.Type{Name: "AckUnion", Kind: ir.TypeLazyUnion, Branches: []ir.UnionBranch{
					{Name: "Primary", GoType: "AckPrimary", TokenClass: ir.TokenObject},
					{Name: "Disc", GoType: "AckDisc", TokenClass: ir.TokenObject, Required: []string{"status"}},
				}}
				return newClassifySpec(primary, disc, union), union
			},
			// permissiveCount == 1 -> warn path; not caller-keyed -> no merge, no lazy.
		},
		{
			name: "multi-key probe rejects merge when another branch carries the same set",
			setup: func() (*ir.Spec, *ir.Type) {
				// Primary is permissive. Branch B requires both x and y.
				// Branch C also has x and y as fields (not required), so the
				// multi-key probe set {x,y} is a subset of C's tags and the
				// disjointness guard at L279-292 rejects the merge.
				primary := structType("Primary", field("ID", "id", "string"))
				bBranch := structType("HasXY",
					field("X", "x", "int"),
					field("Y", "y", "int"),
				)
				cBranch := structType("AlsoXY",
					field("X", "x", "int"),
					field("Y", "y", "int"),
					field("Z", "z", "int"),
				)
				union := &ir.Type{Name: "AmbigUnion", Kind: ir.TypeLazyUnion, Branches: []ir.UnionBranch{
					{Name: "Primary", GoType: "Primary", TokenClass: ir.TokenObject},
					{Name: "HasXY", GoType: "HasXY", TokenClass: ir.TokenObject, Required: []string{"x", "y"}},
					{Name: "AlsoXY", GoType: "AlsoXY", TokenClass: ir.TokenObject, Required: []string{"z"}},
				}}
				return newClassifySpec(primary, bBranch, cBranch, union), union
			},
			// merge refused because {x,y} is a subset of AlsoXY's tags.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, union := tt.setup()
			classifyUnions(spec)

			if got := union.Merge != nil; got != tt.wantMerge {
				t.Fatalf("Merge present = %v, want %v (Merge=%+v)", got, tt.wantMerge, union.Merge)
			}
			if union.LazyAccessors != tt.wantLazy {
				t.Errorf("LazyAccessors = %v, want %v", union.LazyAccessors, tt.wantLazy)
			}
			if !tt.wantMerge {
				return
			}
			if union.Merge.PrimaryGoType != tt.wantPrimary {
				t.Errorf("primary = %q, want %q", union.Merge.PrimaryGoType, tt.wantPrimary)
			}
			probes := make([]string, len(union.Merge.Probes))
			for i, p := range union.Merge.Probes {
				probes[i] = p.JSONKey
			}
			if !slices.Equal(probes, tt.wantProbes) {
				t.Errorf("probe keys = %v, want %v", probes, tt.wantProbes)
			}
			branches := make([]string, len(union.Merge.Branches))
			for i, b := range union.Merge.Branches {
				branches[i] = b.GoType
			}
			if !slices.Equal(branches, tt.wantBranches) {
				t.Errorf("discriminated branches = %v, want %v", branches, tt.wantBranches)
			}
		})
	}
}

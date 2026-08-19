// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"bytes"
	"log"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
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

		wantMerge           bool
		wantRequestSelected bool
		wantPrimary         string   // expected embedded primary GoType (when wantMerge)
		wantProbes          []string // expected probe JSON keys (when wantMerge)
		wantBranches        []string // expected discriminated branch GoTypes (when wantMerge)
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
				union := &ir.Type{Name: "DocsItem", Kind: ir.TypeAmbiguousWire, Branches: []ir.UnionBranch{
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
				union := &ir.Type{Name: "StatusOrException", Kind: ir.TypeAmbiguousWire, Branches: []ir.UnionBranch{
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
				union := &ir.Type{Name: "AggValue", Kind: ir.TypeAmbiguousWire, Branches: []ir.UnionBranch{
					{Name: "Avg", GoType: "AvgAgg", TokenClass: ir.TokenObject},
					{Name: "Sum", GoType: "SumAgg", TokenClass: ir.TokenObject},
				}}
				resp := structType("SearchResult", field("Aggregations", "aggregations", "map[string]AggValue"))
				return newClassifySpec(avg, sum, union, resp), union
			},
			wantRequestSelected: true,
		},
		{
			name: "non-map all-permissive union left on try-each",
			setup: func() (*ir.Spec, *ir.Type) {
				a := structType("ShapeA", field("X", "x", "int"))
				b := structType("ShapeB", field("Y", "y", "int"))
				union := &ir.Type{Name: "DirectBody", Kind: ir.TypeAmbiguousWire, Branches: []ir.UnionBranch{
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
				union := &ir.Type{Name: "OpenItem", Kind: ir.TypeAmbiguousWire, Branches: []ir.UnionBranch{
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
				union := &ir.Type{Name: "MetricAgg", Kind: ir.TypeAmbiguousWire, Branches: []ir.UnionBranch{
					{Name: "Avg", GoType: "AvgAgg", TokenClass: ir.TokenObject, Required: []string{"value"}},
					{Name: "Sum", GoType: "SumAgg", TokenClass: ir.TokenObject, Required: []string{"value"}},
				}}
				resp := structType("SearchResult2", field("Aggregations", "aggregations", "map[string]MetricAgg"))
				return newClassifySpec(avg, sum, union, resp), union
			},
			wantRequestSelected: true,
		},
		{
			name: "non-map branches sharing a required key are left on try-each",
			setup: func() (*ir.Spec, *ir.Type) {
				a := structType("VariantA", field("Type", "type", "string"), field("A", "a", "int"))
				b := structType("VariantB", field("Type", "type", "string"), field("B", "b", "int"))
				union := &ir.Type{Name: "TypeTagged", Kind: ir.TypeAmbiguousWire, Branches: []ir.UnionBranch{
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
				union := &ir.Type{Name: "ObjectOrString", Kind: ir.TypeAmbiguousWire, Branches: []ir.UnionBranch{
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
				union := &ir.Type{Name: "AckUnion", Kind: ir.TypeAmbiguousWire, Branches: []ir.UnionBranch{
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
				union := &ir.Type{Name: "AmbigUnion", Kind: ir.TypeAmbiguousWire, Branches: []ir.UnionBranch{
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
			if union.RequestSelected != tt.wantRequestSelected {
				t.Errorf("RequestSelected = %v, want %v", union.RequestSelected, tt.wantRequestSelected)
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

// TestDropUnreachableBranches pins the phase-ordering rule that a branch's
// reachability is only decidable once a union has reached its terminal decode
// state.
//
// A wire-decoded union walks its branches and stops at the first that decodes, so
// a second branch of the same Go type can never be the one Type() reports. A
// caller-keyed lazy union is the opposite: UnmarshalJSON keeps only the raw
// bytes and the caller names the branch, so every As<Branch>() accessor is
// reachable even when several decode the same Go type. The spec relies on that --
// AvgAggregate, SumAggregate, MinAggregate and five siblings are separate schemas
// that all erase to SingleMetricAggregateBase, and dropping the duplicates would
// delete AsSum/AsMin/... entirely.
func TestDropUnreachableBranches(t *testing.T) {
	// Eight spec schemas erasing to one Go type, as the aggregation families do.
	metricBranches := func() []ir.UnionBranch {
		names := []string{"Avg", "Sum", "Min", "Max", "ValueCount", "WeightedAvg", "SimpleValue", "MedianAbsoluteDeviation"}
		branches := make([]ir.UnionBranch, len(names))
		for i, n := range names {
			branches[i] = ir.UnionBranch{Name: n, GoType: "SingleMetricAggregateBase", TokenClass: ir.TokenObject}
		}
		return branches
	}

	tests := []struct {
		name       string
		union      *ir.Type
		wantBranch []string // expected branch Names after the drop
	}{
		{
			name: "lazy union keeps every accessor over one Go type",
			union: &ir.Type{
				Name:            "AggValue",
				Kind:            ir.TypeAmbiguousWire,
				RequestSelected: true,
				Branches:        metricBranches(),
			},
			wantBranch: []string{"Avg", "Sum", "Min", "Max", "ValueCount", "WeightedAvg", "SimpleValue", "MedianAbsoluteDeviation"},
		},
		{
			name: "wire-decoded union drops the unreachable duplicates",
			union: &ir.Type{
				Name:     "TryEachValue",
				Kind:     ir.TypeAmbiguousWire,
				Branches: metricBranches(),
			},
			wantBranch: []string{"Avg"},
		},
		{
			name: "distinct Go types are never dropped",
			union: &ir.Type{
				Name: "Mixed",
				Kind: ir.TypeUnion,
				Branches: []ir.UnionBranch{
					{Name: "Str", GoType: "string", TokenClass: ir.TokenString},
					{Name: "Num", GoType: "float64", TokenClass: ir.TokenNumber},
				},
			},
			wantBranch: []string{"Str", "Num"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dropUnreachableBranches([]*ir.Type{tt.union})

			got := make([]string, len(tt.union.Branches))
			for i, b := range tt.union.Branches {
				got[i] = b.Name
			}
			if !slices.Equal(got, tt.wantBranch) {
				t.Errorf("branch names = %v, want %v", got, tt.wantBranch)
			}
		})
	}
}

// TestBranchesSharingRequiredKeys covers the probe-collision report: a try-each
// decoder picks a branch by the required keys present in the payload, so two
// branches declaring the same set leave the later one unreachable on decode
// (DistanceFeatureQuery's geo and date forms both require field/origin/pivot).
// Key order must not matter, and permissive branches are decoded by attempt
// rather than by probe, so they never collide.
func TestBranchesSharingRequiredKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		branches []ir.UnionBranch
		want     []string
	}{
		{
			name: "distinct required sets do not collide",
			branches: []ir.UnionBranch{
				{Name: "Doc", Required: []string{"doc"}},
				{Name: "Script", Required: []string{"script"}},
			},
		},
		{
			name: "same required set shadows the later branch",
			branches: []ir.UnionBranch{
				{Name: "Object0", Required: []string{"field", "origin", "pivot"}},
				{Name: "Object1", Required: []string{"field", "origin", "pivot"}},
			},
			want: []string{"Object1 (same required keys as Object0)"},
		},
		{
			name: "required key order is irrelevant",
			branches: []ir.UnionBranch{
				{Name: "Object0", Required: []string{"pivot", "field", "origin"}},
				{Name: "Object1", Required: []string{"field", "origin", "pivot"}},
			},
			want: []string{"Object1 (same required keys as Object0)"},
		},
		{
			name: "permissive branches never collide",
			branches: []ir.UnionBranch{
				{Name: "ShapeA"},
				{Name: "ShapeB"},
			},
		},
		{
			name: "a subset is still distinguishable",
			branches: []ir.UnionBranch{
				{Name: "Object0", Required: []string{"field"}},
				{Name: "Object1", Required: []string{"field", "origin"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := branchesSharingRequiredKeys(&ir.Type{Name: "U", Branches: tt.branches})
			require.Equal(t, tt.want, got)
		})
	}
}

// TestClassifyUnionsReportsBothDiagnostics covers a union that trips both
// diagnostics: it has one embeddable permissive branch (so no merge is safe) and
// two branches sharing a required key (so the probe cannot separate them). They are
// reported by separate passes, and both must reach the log for the same union.
//
// One scenario with no varying input, so there is no table to build, and it cannot
// run in parallel: asserting on log output means installing a process-wide writer.
func TestClassifyUnionsReportsBothDiagnostics(t *testing.T) {
	permissive := structType("Permissive", field("Note", "note", "string"))
	first := structType("First", field("Field", "field", "string"))
	second := structType("Second", field("Field", "field", "string"))

	// One embeddable permissive branch plus two branches whose required sets are
	// identical: the first condition fires on the permissive branch, the second on
	// the duplicated probe.
	union := &ir.Type{Name: "BothDiagnostics", Kind: ir.TypeAmbiguousWire, Branches: []ir.UnionBranch{
		{Name: "Permissive", GoType: "Permissive", TokenClass: ir.TokenObject},
		{Name: "First", GoType: "First", TokenClass: ir.TokenObject, Required: []string{"field"}},
		{Name: "Second", GoType: "Second", TokenClass: ir.TokenObject, Required: []string{"field"}},
	}}
	resp := structType("BothResp", field("Body", "body", "BothDiagnostics"))

	var buf bytes.Buffer
	flags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(flags)
	})

	classifyUnions(newClassifySpec(permissive, first, second, union, resp))

	out := buf.String()
	require.Contains(t, out, `union "BothDiagnostics" left on try-each`)
	require.Contains(t, out, `Second (same required keys as First)`)
}

// TestReportProbeCollisionsPopulatesIR pins the two halves the emitter depends on:
// the colliding branch names land on the IR type (the template renders them), and
// the report is not gated on every branch being an object. GeospatialGeoShapes is
// the real case that gate hid: six object branches all requiring
// {coordinates,type} alongside a permissive array branch.
func TestReportProbeCollisionsPopulatesIR(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func() *ir.Type
		want  []string
	}{
		{
			name: "object branches only",
			setup: func() *ir.Type {
				return &ir.Type{Name: "TwoShapes", Kind: ir.TypeAmbiguousWire, Branches: []ir.UnionBranch{
					{Name: "First", GoType: "First", TokenClass: ir.TokenObject, Required: []string{"field"}},
					{Name: "Second", GoType: "Second", TokenClass: ir.TokenObject, Required: []string{"field"}},
				}}
			},
			want: []string{"Second (same required keys as First)"},
		},
		{
			name: "a non-object branch does not suppress the report",
			setup: func() *ir.Type {
				return &ir.Type{Name: "GeoShapes", Kind: ir.TypeAmbiguousWire, Branches: []ir.UnionBranch{
					{Name: "Point", GoType: "Point", TokenClass: ir.TokenObject, Required: []string{"coordinates", "type"}},
					{Name: "MultiPoint", GoType: "MultiPoint", TokenClass: ir.TokenObject, Required: []string{"coordinates", "type"}},
					{Name: "Array", GoType: "[][]float64", TokenClass: ir.TokenArray},
				}}
			},
			want: []string{"MultiPoint (same required keys as Point)"},
		},
		{
			name: "distinct required sets report nothing",
			setup: func() *ir.Type {
				return &ir.Type{Name: "Distinct", Kind: ir.TypeAmbiguousWire, Branches: []ir.UnionBranch{
					{Name: "Doc", GoType: "Doc", TokenClass: ir.TokenObject, Required: []string{"doc"}},
					{Name: "Script", GoType: "Script", TokenClass: ir.TokenObject, Required: []string{"script"}},
				}}
			},
		},
		{
			name: "a discriminated union names its branch, so probes are irrelevant",
			setup: func() *ir.Type {
				return &ir.Type{
					Name: "Discriminated", Kind: ir.TypeAmbiguousWire,
					Discriminator: &ir.UnionDiscriminator{PropertyName: "type"},
					Branches: []ir.UnionBranch{
						{Name: "First", GoType: "First", TokenClass: ir.TokenObject, Required: []string{"field"}},
						{Name: "Second", GoType: "Second", TokenClass: ir.TokenObject, Required: []string{"field"}},
					},
				}
			},
		},
		{
			name: "a request-selected union is chosen by the caller, not by probe",
			setup: func() *ir.Type {
				return &ir.Type{
					Name: "RequestPicked", Kind: ir.TypeAmbiguousWire, RequestSelected: true,
					Branches: []ir.UnionBranch{
						{Name: "Avg", GoType: "Avg", TokenClass: ir.TokenObject, Required: []string{"value"}},
						{Name: "Sum", GoType: "Sum", TokenClass: ir.TokenObject, Required: []string{"value"}},
					},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			typ := tt.setup()
			reportProbeCollisions([]*ir.Type{typ})
			require.Equal(t, tt.want, typ.ProbeCollisionBranches)
		})
	}
}

// A union reaches the IR as several instances: the shared registry copy plus one
// per operation that references it. Every instance must carry the collision so the
// emitter renders the caveat wherever it writes the type, while the diagnostic is
// logged once. Not parallel: it captures the process-wide log writer.
func TestReportProbeCollisionsWarnsOncePerUnion(t *testing.T) {
	branches := []ir.UnionBranch{
		{Name: "First", GoType: "First", TokenClass: ir.TokenObject, Required: []string{"field"}},
		{Name: "Second", GoType: "Second", TokenClass: ir.TokenObject, Required: []string{"field"}},
	}
	instances := []*ir.Type{
		{Name: "TwoShapes", Kind: ir.TypeAmbiguousWire, Branches: branches},
		{Name: "TwoShapes", Kind: ir.TypeAmbiguousWire, Branches: branches},
		{Name: "TwoShapes", Kind: ir.TypeAmbiguousWire, Branches: branches},
	}

	var buf bytes.Buffer
	flags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(flags)
	})

	reportProbeCollisions(instances)

	for i, typ := range instances {
		require.Equal(t, []string{"Second (same required keys as First)"}, typ.ProbeCollisionBranches,
			"instance %d must carry the collision for the emitter to render it", i)
	}
	require.Equal(t, 1, strings.Count(buf.String(), `union "TwoShapes"`),
		"the diagnostic is reported once per union, not once per instance")
}

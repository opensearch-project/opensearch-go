// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/cmd/osgen/v5/ir"
)

func TestCollectMissingDescriptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		spec *ir.Spec
		want []string // expected report names, in returned (sorted) order
	}{
		{
			name: "type with no description",
			spec: &ir.Spec{Types: []*ir.Type{{
				Name: "SearchHit", SchemaRef: "_core.search___Hit", Scope: ir.ScopeShared,
			}}},
			want: []string{"SearchHit"},
		},
		{
			name: "type with a description is not reported",
			spec: &ir.Spec{Types: []*ir.Type{{
				Name: "SearchHit", SchemaRef: "_core.search___Hit", Scope: ir.ScopeShared,
				Comment: "A single search hit.",
			}}},
			want: nil,
		},
		{
			name: "field with no description",
			spec: &ir.Spec{Types: []*ir.Type{{
				Name: "SearchHit", Scope: ir.ScopeShared, Comment: "A single search hit.",
				Fields: []ir.Field{{GoName: "Index", JSONName: "_index", GoType: "string"}},
			}}},
			want: []string{"SearchHit.Index"},
		},
		{
			name: "field with a description is not reported",
			spec: &ir.Spec{Types: []*ir.Type{{
				Name: "SearchHit", Scope: ir.ScopeShared, Comment: "A single search hit.",
				Fields: []ir.Field{
					{GoName: "Index", JSONName: "_index", GoType: "string", Comment: "The index name."},
					{GoName: "Score", JSONName: "_score", GoType: "float64"},
				},
			}}},
			want: []string{"SearchHit.Score"},
		},
		{
			name: "whitespace-only description counts as missing",
			spec: &ir.Spec{Types: []*ir.Type{{
				Name: "SearchHit", Scope: ir.ScopeShared, Comment: "A single search hit.",
				Fields: []ir.Field{{GoName: "Index", JSONName: "_index", GoType: "string", Comment: "  \n\t"}},
			}}},
			want: []string{"SearchHit.Index"},
		},
		{
			name: "embedded and unnamed fields are skipped",
			spec: &ir.Spec{Types: []*ir.Type{{
				Name: "Embedder", Scope: ir.ScopeShared, Comment: "Embeds another type.",
				Fields: []ir.Field{
					{IsEmbed: true, GoType: "GetResult"},
					{GoName: "", JSONName: "hidden", GoType: "string"},
					{GoName: "Kept", JSONName: "kept", GoType: "string"},
				},
			}}},
			want: []string{"Embedder.Kept"},
		},
		{
			name: "string-enum member with no description",
			spec: &ir.Spec{Types: []*ir.Type{{
				Name: "RestStatus", Scope: ir.ScopeShared, Kind: ir.TypeStringEnum,
				Comment: "An HTTP status.",
				EnumMembers: []ir.EnumMember{
					{ConstName: "RestStatusOK", Value: "OK", Comment: "Success."},
					{ConstName: "RestStatusNotFound", Value: "NOT_FOUND"},
				},
			}}},
			want: []string{"RestStatus.RestStatusNotFound"},
		},
		{
			// The int-backed enum path carries no per-member descriptions at all
			// (convertEnumMembers supplies value-only entries), so reporting its
			// members would be pure noise.
			name: "int-backed enum members are not reported",
			spec: &ir.Spec{Types: []*ir.Type{{
				Name: "Level", Scope: ir.ScopeShared, Kind: ir.TypeEnum, Comment: "A level.",
				EnumMembers: []ir.EnumMember{{ConstName: "LevelHigh", Value: "high"}},
			}}},
			want: nil,
		},
		{
			// A Resp struct's doc comment comes from the operation description,
			// so a described operation with an empty Comment is fully documented.
			name: "resp type uses the operation description",
			spec: &ir.Spec{Operations: []*ir.Operation{{
				Group: "search", TypePrefix: "Search", Description: "Runs a search.",
				Response: &ir.Type{Name: "SearchResp", Comment: ""},
			}}},
			want: nil,
		},
		{
			name: "resp type with no operation description",
			spec: &ir.Spec{Operations: []*ir.Operation{{
				Group: "search", TypePrefix: "Search",
				Response: &ir.Type{Name: "SearchResp"},
			}}},
			want: []string{"SearchResp"},
		},
		{
			name: "request body and sibling types are walked",
			spec: &ir.Spec{Operations: []*ir.Operation{{
				Group: "search", TypePrefix: "Search", Description: "Runs a search.",
				Response:        &ir.Type{Name: "SearchResp"},
				RequestBody:     &ir.Type{Name: "SearchBody", Comment: "The search body."},
				SiblingTypes:    []*ir.Type{{Name: "SearchProfile"}},
				ReqBodySiblings: []*ir.Type{{Name: "SearchAgg", Comment: "An aggregation."}},
			}}},
			want: []string{"SearchProfile"},
		},
		{
			// A registry entry no operation claims never becomes a Go type, so a
			// description gap there is not actionable upstream.
			name: "unclaimed local registry types are skipped",
			spec: &ir.Spec{Types: []*ir.Type{
				{Name: "MultisearchBody", SchemaRef: "_core.msearch___MultisearchBody", Scope: ir.ScopeLocal},
				{Name: "Claimed", SchemaRef: "_common___Claimed", Scope: ir.ScopeShared},
			}},
			want: []string{"Claimed"},
		},
		{
			name: "a type reached twice is reported once",
			spec: &ir.Spec{
				Operations: []*ir.Operation{
					{Group: "a", Description: "A.", SiblingTypes: []*ir.Type{{Name: "Dup", Fields: []ir.Field{{GoName: "F", JSONName: "f"}}}}},
					{Group: "b", Description: "B.", SiblingTypes: []*ir.Type{{Name: "Dup", Fields: []ir.Field{{GoName: "F", JSONName: "f"}}}}},
				},
				Types: []*ir.Type{{Name: "Dup", Scope: ir.ScopeShared, Fields: []ir.Field{{GoName: "F", JSONName: "f"}}}},
			},
			want: []string{"Dup", "Dup.F"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			entries := collectMissingDescriptions(tt.spec)
			got := make([]string, len(entries))
			for i, e := range entries {
				got[i] = e.name()
			}
			if len(tt.want) == 0 {
				require.Empty(t, got)
				return
			}
			require.Equal(t, tt.want, got)
		})
	}
}

// TestCollectMissingDescriptions_Deterministic pins the ordering contract: the
// spec is parsed into Go maps, so a walk that did not sort would emit a
// different report on every run and make the output useless as a diff target.
func TestCollectMissingDescriptions_Deterministic(t *testing.T) {
	t.Parallel()

	// Deliberately unsorted, and mixing all three kinds.
	spec := &ir.Spec{Types: []*ir.Type{
		{
			Name: "Zeta", Scope: ir.ScopeShared, Kind: ir.TypeStringEnum,
			EnumMembers: []ir.EnumMember{{ConstName: "ZetaB", Value: "b"}, {ConstName: "ZetaA", Value: "a"}},
		},
		{
			Name: "Alpha", Scope: ir.ScopeShared,
			Fields: []ir.Field{{GoName: "Zulu", JSONName: "zulu"}, {GoName: "Bravo", JSONName: "bravo"}},
		},
		{Name: "Mid", Scope: ir.ScopeShared},
	}}

	entries := collectMissingDescriptions(spec)
	got := make([]string, len(entries))
	for i, e := range entries {
		got[i] = e.name()
	}
	require.Equal(t, []string{
		// Types first, sorted.
		"Alpha", "Mid", "Zeta",
		// Then fields, sorted by owning type then Go name.
		"Alpha.Bravo", "Alpha.Zulu",
		// Then enum members.
		"Zeta.ZetaA", "Zeta.ZetaB",
	}, got)

	// Reversing the input must not change the output.
	reversed := &ir.Spec{Types: []*ir.Type{spec.Types[2], spec.Types[1], spec.Types[0]}}
	var first, second bytes.Buffer
	reportMissingDescriptions(&first, spec, DescriptionReportConfig{Report: true})
	reportMissingDescriptions(&second, reversed, DescriptionReportConfig{Report: true})
	require.Equal(t, first.String(), second.String())
}

func TestReportMissingDescriptions(t *testing.T) {
	t.Parallel()

	spec := &ir.Spec{Types: []*ir.Type{
		{
			Name: "SearchHit", SchemaRef: "_core.search___Hit", Scope: ir.ScopeShared,
			Fields: []ir.Field{
				{GoName: "Index", JSONName: "_index", Comment: "The index name."},
				{GoName: "Score", JSONName: "_score"},
			},
		},
		{
			Name: "RestStatus", SchemaRef: "_common___RestStatus", Scope: ir.ScopeShared,
			Kind: ir.TypeStringEnum, Comment: "An HTTP status.",
			EnumMembers: []ir.EnumMember{{ConstName: "RestStatusNotFound", Value: "NOT_FOUND"}},
		},
	}}

	tests := []struct {
		name          string
		cfg           DescriptionReportConfig
		spec          *ir.Spec
		wantSubstr    []string
		wantAbsent    []string
		wantAnyOutput bool
	}{
		{
			name: "disabled writes nothing",
			cfg:  DescriptionReportConfig{},
			spec: spec,
		},
		{
			name: "enabled groups by kind with wire names and counts",
			cfg:  DescriptionReportConfig{Report: true},
			spec: spec,
			wantSubstr: []string{
				"types with no description (1)",
				"- SearchHit [_core.search___Hit]",
				"struct fields with no description (1)",
				`- SearchHit.Score json:"_score" [_core.search___Hit]`,
				"enum members with no description (1)",
				`- RestStatus.RestStatusNotFound value:"NOT_FOUND" [_common___RestStatus]`,
				"SUMMARY: 1 type, 1 field, 1 enum member; 3 total",
			},
			// A described field must never appear.
			wantAbsent:    []string{"SearchHit.Index"},
			wantAnyOutput: true,
		},
		{
			name: "fully documented spec reports no gaps",
			cfg:  DescriptionReportConfig{Report: true},
			spec: &ir.Spec{Types: []*ir.Type{{
				Name: "Documented", Scope: ir.ScopeShared, Comment: "Has a description.",
				Fields: []ir.Field{{GoName: "F", JSONName: "f", Comment: "A field."}},
			}}},
			wantSubstr:    []string{"every generated type, field, and enum member has a description."},
			wantAbsent:    []string{"SUMMARY"},
			wantAnyOutput: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			reportMissingDescriptions(&out, tt.spec, tt.cfg)
			if !tt.wantAnyOutput {
				require.Empty(t, out.String())
				return
			}
			for _, sub := range tt.wantSubstr {
				require.Contains(t, out.String(), sub)
			}
			for _, sub := range tt.wantAbsent {
				require.NotContains(t, out.String(), sub)
			}
			// Sections must appear in kind order.
			text := out.String()
			if strings.Contains(text, "enum members with no description") {
				require.Less(t, strings.Index(text, "types with no description"),
					strings.Index(text, "struct fields with no description"))
				require.Less(t, strings.Index(text, "struct fields with no description"),
					strings.Index(text, "enum members with no description"))
			}
		})
	}
}

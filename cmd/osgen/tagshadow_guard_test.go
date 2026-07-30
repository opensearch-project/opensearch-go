// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

// embed returns an embedded field, which is how the IR spells struct embedding:
// a field with no JSON tag whose GoType names the embedded type.
func embed(goType string) ir.Field {
	return ir.Field{GoType: goType, IsEmbed: true}
}

// tagged returns a normal wire-facing field.
func tagged(goName, jsonName, goType string) ir.Field {
	return ir.Field{GoName: goName, JSONName: jsonName, GoType: goType}
}

func TestClassifyShadow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		outer      string
		shadowed   string
		want       shadowKind
		wantString string
	}{
		{
			name: "concrete over erased union is a narrowing", outer: "[]CommonAggregationsStringTermsBucket",
			shadowed: "CommonAggregationsMultiBucketAggregateBaseBuckets",
			want:     shadowNarrowing, wantString: shadowKindLabelNarrowing,
		},
		{
			name: "raw over typed erases", outer: "json.RawMessage", shadowed: "SearchHit",
			want: shadowErased, wantString: shadowKindLabelErased,
		},
		{
			name: "raw slice over typed slice erases", outer: "[]json.RawMessage", shadowed: "[]SearchHit",
			want: shadowErased, wantString: shadowKindLabelErased,
		},
		{
			name: "identical types are redundant", outer: "SearchHit", shadowed: "SearchHit",
			want: shadowRedundant, wantString: shadowKindLabelRedundant,
		},
		{
			name: "raw over raw is not an erasure", outer: "json.RawMessage", shadowed: "map[string]json.RawMessage",
			want: shadowNarrowing, wantString: shadowKindLabelNarrowing,
		},
		{
			name: "typed over raw is a narrowing", outer: "SearchHit", shadowed: "json.RawMessage",
			want: shadowNarrowing, wantString: shadowKindLabelNarrowing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := classifyShadow(tt.outer, tt.shadowed)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.wantString, got.String())
		})
	}
}

func TestCollectTagShadows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		spec *ir.Spec
		want []string // expected keys, in returned (sorted) order
	}{
		{
			// The search-envelope regression: the outer struct redeclares the
			// embed's tag with a payload that carries less.
			name: "outer field shadows a direct embed",
			spec: &ir.Spec{Types: []*ir.Type{
				{Name: "SearchResultHits", Fields: []ir.Field{
					embed("SearchHitsMetadata"),
					tagged("Hits", "hits", "[]SearchResultHitsHitsItem"),
				}},
				{Name: "SearchHitsMetadata", Fields: []ir.Field{
					tagged("Hits", "hits", "[]SearchHit"),
					tagged("MaxScore", "max_score", "*float32"),
				}},
			}},
			want: []string{"SearchResultHits/hits/SearchHitsMetadata"},
		},
		{
			// CommonAggregationsStringTermsAggregate reaches `buckets` two hops
			// in, so a one-level check would miss it entirely.
			name: "shadow through a two-hop embed chain",
			spec: &ir.Spec{Types: []*ir.Type{
				{Name: "StringTermsAggregate", Fields: []ir.Field{
					embed("TermsAggregateBase"),
					tagged("Buckets", "buckets", "[]StringTermsBucket"),
				}},
				{Name: "TermsAggregateBase", Fields: []ir.Field{
					embed("MultiBucketAggregateBase"),
					tagged("SumOtherDocCount", "sum_other_doc_count", "*int64"),
				}},
				{Name: "MultiBucketAggregateBase", Fields: []ir.Field{
					tagged("Buckets", "buckets", "MultiBucketAggregateBaseBuckets"),
				}},
			}},
			want: []string{"StringTermsAggregate/buckets/MultiBucketAggregateBase"},
		},
		{
			// Only the shallowest declaration of a tag loses to the outer field:
			// that is the one encoding/json would otherwise have picked. Mid is a
			// rendered type in its own right, so its own shadow of Inner is a
			// separate finding rather than a duplicate of Outer's.
			name: "shallowest declaration in the chain is the one reported",
			spec: &ir.Spec{Types: []*ir.Type{
				{Name: "Outer", Fields: []ir.Field{
					embed("Mid"),
					tagged("X", "x", "string"),
				}},
				{Name: "Mid", Fields: []ir.Field{
					embed("Inner"),
					tagged("X", "x", "int"),
				}},
				{Name: "Inner", Fields: []ir.Field{
					tagged("X", "x", "bool"),
				}},
			}},
			want: []string{"Mid/x/Inner", "Outer/x/Mid"},
		},
		{
			name: "no collision is not reported",
			spec: &ir.Spec{Types: []*ir.Type{
				{Name: "Outer", Fields: []ir.Field{
					embed("Base"),
					tagged("Extra", "extra", "string"),
				}},
				{Name: "Base", Fields: []ir.Field{tagged("Hits", "hits", "[]SearchHit")}},
			}},
			want: nil,
		},
		{
			// Two embeds colliding at equal depth drop both fields, which is a
			// different failure with a different fix; this guard does not claim it.
			name: "sibling embeds colliding at equal depth are not this guard's subject",
			spec: &ir.Spec{Types: []*ir.Type{
				{Name: "Outer", Fields: []ir.Field{embed("A"), embed("B")}},
				{Name: "A", Fields: []ir.Field{tagged("X", "x", "string")}},
				{Name: "B", Fields: []ir.Field{tagged("X", "x", "int")}},
			}},
			want: nil,
		},
		{
			name: "unresolvable embed contributes nothing",
			spec: &ir.Spec{Types: []*ir.Type{
				{Name: "Outer", Fields: []ir.Field{
					embed("NotInTheIR"),
					tagged("Hits", "hits", "[]SearchHit"),
				}},
			}},
			want: nil,
		},
		{
			// A tag of "-" is never a wire name, and an unnamed field is not
			// rendered, so neither can shadow or be shadowed.
			name: "untagged and unnamed fields are skipped",
			spec: &ir.Spec{Types: []*ir.Type{
				{Name: "Outer", Fields: []ir.Field{
					embed("Base"),
					{GoName: "Body", JSONName: "-", GoType: "json.RawMessage"},
					{GoName: "", JSONName: "x", GoType: "string"},
				}},
				{Name: "Base", Fields: []ir.Field{
					{GoName: "Body", JSONName: "-", GoType: "json.RawMessage"},
					{GoName: "", JSONName: "x", GoType: "string"},
				}},
			}},
			want: nil,
		},
		{
			// A cyclic embed cannot arise from the spec, but the walk must
			// terminate rather than depend on that.
			name: "cyclic embed terminates",
			spec: &ir.Spec{Types: []*ir.Type{
				{Name: "A", Fields: []ir.Field{embed("B"), tagged("X", "x", "string")}},
				{Name: "B", Fields: []ir.Field{embed("A"), tagged("X", "x", "int")}},
			}},
			want: []string{"A/x/B", "B/x/A"},
		},
		{
			// Response, request-body, and sibling types are all emitted, so all
			// are walked. The same type reaching the walk twice yields one entry.
			name: "operation-owned types are walked and deduplicated",
			spec: &ir.Spec{
				Operations: []*ir.Operation{{
					Group: "search",
					Response: &ir.Type{Name: "SearchResp", SchemaRef: "_core.search___SearchResp", Fields: []ir.Field{
						embed("Base"),
						tagged("Hits", "hits", "json.RawMessage"),
					}},
					RequestBody: &ir.Type{Name: "SearchBody", Fields: []ir.Field{
						embed("Base"),
						tagged("Hits", "hits", "int"),
					}},
					SiblingTypes: []*ir.Type{
						{Name: "Base", Fields: []ir.Field{tagged("Hits", "hits", "[]SearchHit")}},
					},
				}},
				Types: []*ir.Type{
					{Name: "Base", Fields: []ir.Field{tagged("Hits", "hits", "[]SearchHit")}},
				},
			},
			want: []string{"SearchBody/hits/Base", "SearchResp/hits/Base"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			shadows := collectTagShadows(tt.spec)
			got := make([]string, len(shadows))
			for i, s := range shadows {
				got[i] = s.key()
			}
			// Order matters (collectTagShadows sorts), so compare in order;
			// treat nil and empty as equivalent for the no-results cases.
			if len(tt.want) == 0 {
				require.Empty(t, got)
				return
			}
			require.Equal(t, tt.want, got)
		})
	}
}

// TestCollectTagShadows_Detail pins the reviewable half of an allowlist entry:
// the two competing types, and the embed path when the shadowed declaration is
// deeper than the embed named on the outer struct.
func TestCollectTagShadows_Detail(t *testing.T) {
	t.Parallel()

	spec := &ir.Spec{Types: []*ir.Type{
		{Name: "StringTermsAggregate", Fields: []ir.Field{
			embed("TermsAggregateBase"),
			tagged("Buckets", "buckets", "[]StringTermsBucket"),
		}},
		{Name: "TermsAggregateBase", Fields: []ir.Field{embed("MultiBucketAggregateBase")}},
		{Name: "MultiBucketAggregateBase", Fields: []ir.Field{
			tagged("Buckets", "buckets", "MultiBucketAggregateBaseBuckets"),
		}},
	}}

	shadows := collectTagShadows(spec)
	require.Len(t, shadows, 1)

	detail := shadows[0].detail()
	require.Contains(t, detail, shadowKindLabelNarrowing)
	require.Contains(t, detail, "[]StringTermsBucket shadows MultiBucketAggregateBase.buckets MultiBucketAggregateBaseBuckets")
	// The declaring type is already named, so the "via" path lists only the hops
	// taken to reach it.
	require.Contains(t, detail, "(via TermsAggregateBase)")
}

func TestLoadTagShadowAllowlist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    []string // sorted keys expected in the set
	}{
		{
			name:    "keys with comments and blanks",
			content: "# header\n\nOuter/hits/Base # narrowing\n  Other/x/Base  # narrowing\n\n# trailing\n",
			want:    []string{"Other/x/Base", "Outer/hits/Base"},
		},
		{
			name:    "group headers ignored",
			content: "# --- _core.search ---\nOuter/hits/Base\n# --- _common.aggregations ---\nAgg/buckets/Base\n",
			want:    []string{"Agg/buckets/Base", "Outer/hits/Base"},
		},
		{
			name:    "duplicate keys collapse",
			content: "Dup/k/Base # first\nDup/k/Base # second\n",
			want:    []string{"Dup/k/Base"},
		},
		{
			name:    "empty file",
			content: "# only comments\n",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "allow.txt")
			require.NoError(t, os.WriteFile(path, []byte(tt.content), 0o600))

			allowed, err := loadTagShadowAllowlist(path)
			require.NoError(t, err)

			got := make([]string, 0, len(allowed))
			for k := range allowed {
				got = append(got, k)
			}
			require.ElementsMatch(t, tt.want, got)
		})
	}
}

func TestLoadTagShadowAllowlist_MissingFile(t *testing.T) {
	t.Parallel()

	_, err := loadTagShadowAllowlist(filepath.Join(t.TempDir(), "does-not-exist.txt"))
	require.Error(t, err)
	require.ErrorContains(t, err, "-update-tagshadow-allowlist")
}

func TestGuardTagShadows(t *testing.T) {
	t.Parallel()

	// specWithOneShadow produces exactly one shadow: the search-envelope shape,
	// keyed SearchResultHits/hits/SearchHitsMetadata.
	specWithOneShadow := func() *ir.Spec {
		return &ir.Spec{Types: []*ir.Type{
			{Name: "SearchResultHits", Fields: []ir.Field{
				embed("SearchHitsMetadata"),
				tagged("Hits", "hits", "[]SearchResultHitsHitsItem"),
			}},
			{Name: "SearchHitsMetadata", Fields: []ir.Field{tagged("Hits", "hits", "[]SearchHit")}},
		}}
	}

	tests := []struct {
		name          string
		allowlist     string // file content; "" means do not create the file
		createFile    bool
		cfg           TagShadowConfig
		wantErr       bool
		wantOutSubstr []string
	}{
		{
			name:       "all shadows allowed passes silently",
			allowlist:  "SearchResultHits/hits/SearchHitsMetadata\n",
			createFile: true,
			wantErr:    false,
		},
		{
			name:          "unlisted shadow is fatal",
			allowlist:     "# empty\n",
			createFile:    true,
			wantErr:       true,
			wantOutSubstr: []string{"WARNING", "SearchResultHits/hits/SearchHitsMetadata"},
		},
		{
			// The declaring type is part of the key, so an entry naming a
			// different base does not carry over to a re-pointed embed.
			name:          "entry for a different declaring type does not apply",
			allowlist:     "SearchResultHits/hits/SomeOtherBase\n",
			createFile:    true,
			wantErr:       true,
			wantOutSubstr: []string{"SearchResultHits/hits/SearchHitsMetadata"},
		},
		{
			name:          "unlisted shadow with bypass is a warning",
			allowlist:     "# empty\n",
			createFile:    true,
			cfg:           TagShadowConfig{AllowUnlisted: true},
			wantErr:       false,
			wantOutSubstr: []string{"continuing despite", "SearchResultHits/hits/SearchHitsMetadata"},
		},
		{
			name:          "stale entry warns but passes",
			allowlist:     "SearchResultHits/hits/SearchHitsMetadata\nGone/old/Base\n",
			createFile:    true,
			wantErr:       false,
			wantOutSubstr: []string{"no longer present", "Gone/old/Base"},
		},
		{
			name:       "missing file is fatal without bypass",
			createFile: false,
			wantErr:    true,
		},
		{
			name:       "missing file with bypass is not fatal",
			createFile: false,
			cfg:        TagShadowConfig{AllowUnlisted: true},
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "allow.txt")
			if tt.createFile {
				require.NoError(t, os.WriteFile(path, []byte(tt.allowlist), 0o600))
			}
			cfg := tt.cfg
			cfg.AllowlistPath = path

			var out bytes.Buffer
			err := guardTagShadows(&out, specWithOneShadow(), cfg)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			for _, sub := range tt.wantOutSubstr {
				require.Contains(t, out.String(), sub)
			}
		})
	}
}

func TestGuardTagShadows_UpdateRoundTrip(t *testing.T) {
	t.Parallel()

	spec := &ir.Spec{Types: []*ir.Type{
		{Name: "SearchResultHits", Fields: []ir.Field{
			embed("SearchHitsMetadata"),
			tagged("Hits", "hits", "[]SearchResultHitsHitsItem"),
		}},
		{Name: "SearchHitsMetadata", Fields: []ir.Field{tagged("Hits", "hits", "[]SearchHit")}},
	}}

	path := filepath.Join(t.TempDir(), "allow.txt")
	cfg := TagShadowConfig{AllowlistPath: path, Update: true}

	var out bytes.Buffer
	require.NoError(t, guardTagShadows(&out, spec, cfg))
	require.FileExists(t, path)

	// The written file must satisfy a subsequent (non-update) check.
	check := TagShadowConfig{AllowlistPath: path}
	require.NoError(t, guardTagShadows(&bytes.Buffer{}, spec, check))

	// And it round-trips to the same shadow set.
	allowed, err := loadTagShadowAllowlist(path)
	require.NoError(t, err)
	require.Contains(t, allowed, "SearchResultHits/hits/SearchHitsMetadata")
}

// TestGuardTagShadows_EmbeddedDefault exercises the default check path: with no
// AllowlistPath the guard consults the allowlist compiled into the binary, from
// any working directory. Chdir'ing into an empty dir first is the point - a
// cwd-relative read of tagshadow_allowlist.txt fails there, so this test breaks
// if the embed is not wired.
func TestGuardTagShadows_EmbeddedDefault(t *testing.T) {
	t.Chdir(t.TempDir()) // t.Chdir forbids t.Parallel

	// SearchResultJSONValue/suggest/SearchResult is a real entry in
	// tagshadow_allowlist.txt.
	spec := &ir.Spec{Types: []*ir.Type{
		{Name: "SearchResultJSONValue", Fields: []ir.Field{
			embed("SearchResult"),
			tagged("Suggest", "suggest", "map[string][]SearchResultJSONValueSuggestValueItem"),
		}},
		{Name: "SearchResult", Fields: []ir.Field{
			tagged("Suggest", "suggest", "map[string][]SearchResultSuggestValueItem"),
		}},
	}}

	var out bytes.Buffer
	require.NoError(t, guardTagShadows(&out, spec, TagShadowConfig{}))
	require.NotContains(t, out.String(), "WARNING")

	// The inverse: a shadow absent from the embedded allowlist is fatal, and both
	// the error and the warning name the embedded list as what was enforced.
	unlisted := &ir.Spec{Types: []*ir.Type{
		{Name: "NotARealGeneratedType", Fields: []ir.Field{
			embed("NotARealBase"),
			tagged("Body", "body", "json.RawMessage"),
		}},
		{Name: "NotARealBase", Fields: []ir.Field{tagged("Body", "body", "SomeTypedPayload")}},
	}}

	out.Reset()
	err := guardTagShadows(&out, unlisted, TagShadowConfig{})
	require.ErrorContains(t, err, "embedded "+tagShadowAllowlistFile)
	require.Contains(t, out.String(), "embedded "+tagShadowAllowlistFile)
	require.Contains(t, out.String(), shadowKindLabelErased)
}

func TestWriteTagShadowAllowlist_StableSorted(t *testing.T) {
	t.Parallel()

	// Three shadows in two groups, deliberately constructed out of order.
	shadows := []tagShadow{
		{Outer: "Zeta", JSONName: "z", Declaring: "ZBase", Chain: []string{"ZBase"}, group: "z_group"},
		{Outer: "Alpha", JSONName: "b", Declaring: "ABase", Chain: []string{"ABase"}, group: "a_group"},
		{Outer: "Alpha", JSONName: "a", Declaring: "ABase", Chain: []string{"ABase"}, group: "a_group"},
	}

	render := func(in []tagShadow) string {
		cp := append([]tagShadow(nil), in...)
		sortTagShadows(cp)
		path := filepath.Join(t.TempDir(), "allow.txt")
		_, err := writeTagShadowAllowlist(path, cp)
		require.NoError(t, err)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		return string(data)
	}

	first := render(shadows)

	// Reversed input must produce byte-identical output (stable sort).
	reversed := []tagShadow{shadows[2], shadows[0], shadows[1]}
	require.Equal(t, first, render(reversed))

	// Keys appear sorted by group then key.
	idxAlphaA := strings.Index(first, "Alpha/a/ABase")
	idxAlphaB := strings.Index(first, "Alpha/b/ABase")
	idxZeta := strings.Index(first, "Zeta/z/ZBase")
	require.Less(t, idxAlphaA, idxAlphaB, "keys sorted within group")
	require.Less(t, idxAlphaB, idxZeta, "groups sorted (a_group before z_group)")
}

// TestCollectTagShadows_Deterministic pins the walk against map iteration order:
// the index it builds is a map, so without an explicit sort the offender list
// would vary from run to run.
func TestCollectTagShadows_Deterministic(t *testing.T) {
	t.Parallel()

	spec := func() *ir.Spec {
		return &ir.Spec{Types: []*ir.Type{
			{Name: "Zeta", Fields: []ir.Field{embed("Base"), tagged("X", "x", "string")}},
			{Name: "Alpha", Fields: []ir.Field{embed("Base"), tagged("X", "x", "int")}},
			{Name: "Mid", Fields: []ir.Field{embed("Base"), tagged("X", "x", "bool")}},
			{Name: "Base", Fields: []ir.Field{tagged("X", "x", "json.RawMessage")}},
		}}
	}

	want := []string{"Alpha/x/Base", "Mid/x/Base", "Zeta/x/Base"}
	for range 20 {
		shadows := collectTagShadows(spec())
		got := make([]string, len(shadows))
		for i, s := range shadows {
			got[i] = s.key()
		}
		require.Equal(t, want, got)
	}
}

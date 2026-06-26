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

func TestClassifyRawForm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		goType   string
		wantForm rawForm
		wantOK   bool
	}{
		{name: "bare", goType: "json.RawMessage", wantForm: rawBare, wantOK: true},
		{name: "pointer bare", goType: "*json.RawMessage", wantForm: rawBare, wantOK: true},
		{name: "slice", goType: "[]json.RawMessage", wantForm: rawSlice, wantOK: true},
		{name: "map", goType: "map[string]json.RawMessage", wantForm: rawMap, wantOK: true},
		// Nested forms report the outermost wrapper. SQL/PPL Datarows are
		// [][]json.RawMessage; these must be detected or they escape the guard.
		{name: "slice of slice", goType: "[][]json.RawMessage", wantForm: rawSlice, wantOK: true},
		{name: "map of slice", goType: "map[string][]json.RawMessage", wantForm: rawMap, wantOK: true},
		{name: "slice of map", goType: "[]map[string]json.RawMessage", wantForm: rawSlice, wantOK: true},
		{name: "pointer slice", goType: "*[]json.RawMessage", wantForm: rawSlice, wantOK: true},
		{name: "typed struct", goType: "SearchHit", wantOK: false},
		{name: "typed slice", goType: "[]SearchHit", wantOK: false},
		{name: "typed map", goType: "map[string]SearchHit", wantOK: false},
		{name: "typed nested slice", goType: "[][]SearchHit", wantOK: false},
		{name: "primitive", goType: "string", wantOK: false},
		{name: "empty", goType: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			form, ok := classifyRawForm(tt.goType)
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				require.Equal(t, tt.wantForm, form)
			}
		})
	}
}

func TestCollectRawMessageUses(t *testing.T) {
	t.Parallel()

	// rawField is a json.RawMessage struct field; typedField is a control that
	// must never be collected.
	rawField := func(json, goType string) ir.Field {
		return ir.Field{GoName: "F", JSONName: json, GoType: goType}
	}

	// sharedType builds a type that appears under multiple ops and spec.Types.
	sharedType := func() *ir.Type {
		return &ir.Type{Name: "Shared", SchemaRef: "_common___Shared", Fields: []ir.Field{rawField("blob", "json.RawMessage")}}
	}

	tests := []struct {
		name string
		spec *ir.Spec
		want []string // expected keys, in returned (sorted) order
	}{
		{
			name: "response and request struct fields",
			spec: &ir.Spec{Operations: []*ir.Operation{{
				Group:      "search",
				TypePrefix: "Search",
				Response: &ir.Type{Name: "SearchResp", Fields: []ir.Field{
					rawField("_source", "json.RawMessage"),
					rawField("hits", "SearchHits"), // typed, ignored
				}},
				RequestBody: &ir.Type{Name: "SearchBody", Fields: []ir.Field{
					rawField("query", "[]json.RawMessage"),
				}},
			}}},
			want: []string{"SearchBody/query", "SearchResp/_source"},
		},
		{
			name: "sibling and reqbody-sibling types",
			spec: &ir.Spec{Operations: []*ir.Operation{{
				Group:           "search",
				SiblingTypes:    []*ir.Type{{Name: "SearchHit", Fields: []ir.Field{rawField("fields", "map[string]json.RawMessage")}}},
				ReqBodySiblings: []*ir.Type{{Name: "SearchAgg", Fields: []ir.Field{rawField("agg", "json.RawMessage")}}},
			}}},
			want: []string{"SearchAgg/agg", "SearchHit/fields"},
		},
		{
			name: "whole-response raw body",
			spec: &ir.Spec{Operations: []*ir.Operation{{
				Group: "reindex", TypePrefix: "Reindex", RespShape: ir.RespShapeRaw,
			}}},
			want: []string{"ReindexResp/-"},
		},
		{
			name: "map response with unresolved element",
			spec: &ir.Spec{Operations: []*ir.Operation{{
				Group: "cat", TypePrefix: "CatIndices", RespShape: ir.RespShapeMap, RespElemType: nil,
			}}},
			want: []string{"CatIndicesResp/[entries]"},
		},
		{
			name: "array response with unresolved element",
			spec: &ir.Spec{Operations: []*ir.Operation{{
				Group: "cat", TypePrefix: "CatNodes", RespShape: ir.RespShapeArray, RespElemType: nil,
			}}},
			want: []string{"CatNodesResp/[records]"},
		},
		{
			name: "map/array response with resolved element is not raw",
			spec: &ir.Spec{Operations: []*ir.Operation{{
				Group: "cat", TypePrefix: "CatX", RespShape: ir.RespShapeMap,
				RespElemType: &ir.Type{Name: "CatXEntry"},
			}}},
			want: nil,
		},
		{
			name: "embedded and unnamed fields are skipped",
			spec: &ir.Spec{Types: []*ir.Type{{
				Name: "Embedder", Fields: []ir.Field{
					{GoName: "", JSONName: "x", GoType: "json.RawMessage"},    // unnamed
					{IsEmbed: true, JSONName: "y", GoType: "json.RawMessage"}, // embedded
					{GoName: "Z", JSONName: "z", GoType: "json.RawMessage"},   // kept
				},
			}}},
			want: []string{"Embedder/z"},
		},
		{
			name: "dedup across operations and spec.Types",
			spec: &ir.Spec{
				Operations: []*ir.Operation{
					{Group: "a", SiblingTypes: []*ir.Type{sharedType()}},
					{Group: "b", SiblingTypes: []*ir.Type{sharedType()}},
				},
				Types: []*ir.Type{sharedType()},
			},
			want: []string{"Shared/blob"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			uses := collectRawMessageUses(tt.spec)
			got := make([]string, len(uses))
			for i, u := range uses {
				got[i] = u.key()
			}
			// Order matters (collectRawMessageUses sorts), so compare in order;
			// treat nil and empty as equivalent for the no-results cases.
			if len(tt.want) == 0 {
				require.Empty(t, got)
				return
			}
			require.Equal(t, tt.want, got)
		})
	}
}

func TestLoadRawMessageAllowlist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    []string // sorted keys expected in the set
	}{
		{
			name:    "keys with comments and blanks",
			content: "# header\n\nSearchHit/_source # bare\n  SearchHit/fields  # map\n\n# trailing\n",
			want:    []string{"SearchHit/_source", "SearchHit/fields"},
		},
		{
			name:    "group headers ignored",
			content: "# --- search ---\nSearchResp/-\n# --- cat ---\nCatNodesResp/[records]\n",
			want:    []string{"CatNodesResp/[records]", "SearchResp/-"},
		},
		{
			name:    "duplicate keys collapse",
			content: "Dup/k # first\nDup/k # second\nDup/k # third\n",
			want:    []string{"Dup/k"},
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

			allowed, err := loadRawMessageAllowlist(path)
			require.NoError(t, err)

			got := make([]string, 0, len(allowed))
			for k := range allowed {
				got = append(got, k)
			}
			require.ElementsMatch(t, tt.want, got)
		})
	}
}

func TestLoadRawMessageAllowlist_MissingFile(t *testing.T) {
	t.Parallel()

	_, err := loadRawMessageAllowlist(filepath.Join(t.TempDir(), "does-not-exist.txt"))
	require.Error(t, err)
	require.ErrorContains(t, err, "-update-raw-message-allowlist")
}

func TestGuardRawMessages(t *testing.T) {
	t.Parallel()

	// specWithOneRaw produces exactly one use: SearchResp/_source (bare).
	specWithOneRaw := func() *ir.Spec {
		return &ir.Spec{Operations: []*ir.Operation{{
			Group: "search", TypePrefix: "Search",
			Response: &ir.Type{Name: "SearchResp", Fields: []ir.Field{
				{GoName: "Source", JSONName: "_source", GoType: "json.RawMessage"},
			}},
		}}}
	}

	tests := []struct {
		name          string
		allowlist     string // file content; "" means do not create the file
		createFile    bool
		cfg           RawMessageConfig
		wantErr       bool
		wantOutSubstr []string
	}{
		{
			name:       "all uses allowed passes silently",
			allowlist:  "SearchResp/_source\n",
			createFile: true,
			wantErr:    false,
		},
		{
			name:          "unlisted use is fatal",
			allowlist:     "# empty\n",
			createFile:    true,
			wantErr:       true,
			wantOutSubstr: []string{"WARNING", "SearchResp/_source"},
		},
		{
			name:          "unlisted use with bypass is a warning",
			allowlist:     "# empty\n",
			createFile:    true,
			cfg:           RawMessageConfig{AllowUnlisted: true},
			wantErr:       false,
			wantOutSubstr: []string{"continuing despite", "SearchResp/_source"},
		},
		{
			name:          "stale entry warns but passes",
			allowlist:     "SearchResp/_source\nGoneResp/old\n",
			createFile:    true,
			wantErr:       false,
			wantOutSubstr: []string{"no longer present", "GoneResp/old"},
		},
		{
			name:       "missing file is fatal without bypass",
			createFile: false,
			wantErr:    true,
		},
		{
			name:       "missing file with bypass is not fatal",
			createFile: false,
			cfg:        RawMessageConfig{AllowUnlisted: true},
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
			err := guardRawMessages(&out, specWithOneRaw(), cfg)
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

func TestGuardRawMessages_UpdateRoundTrip(t *testing.T) {
	t.Parallel()

	spec := &ir.Spec{Operations: []*ir.Operation{{
		Group: "search", TypePrefix: "Search",
		Response: &ir.Type{Name: "SearchResp", Fields: []ir.Field{
			{GoName: "Source", JSONName: "_source", GoType: "json.RawMessage"},
		}},
	}}}

	path := filepath.Join(t.TempDir(), "allow.txt")
	cfg := RawMessageConfig{AllowlistPath: path, Update: true}

	var out bytes.Buffer
	require.NoError(t, guardRawMessages(&out, spec, cfg))
	require.FileExists(t, path)

	// The written file must satisfy a subsequent (non-update) check.
	check := RawMessageConfig{AllowlistPath: path}
	require.NoError(t, guardRawMessages(&bytes.Buffer{}, spec, check))

	// And it round-trips to the same use set.
	allowed, err := loadRawMessageAllowlist(path)
	require.NoError(t, err)
	require.Contains(t, allowed, "SearchResp/_source")
}

func TestWriteRawMessageAllowlist_StableSorted(t *testing.T) {
	t.Parallel()

	// Two uses in different groups, deliberately constructed out of order.
	uses := []rawUse{
		{GoType: "Zeta", JSONName: "z", Form: rawBare, group: "z_group"},
		{GoType: "Alpha", JSONName: "b", Form: rawMap, group: "a_group"},
		{GoType: "Alpha", JSONName: "a", Form: rawBare, group: "a_group"},
	}

	render := func(in []rawUse) string {
		cp := append([]rawUse(nil), in...)
		sortRawUses(cp)
		path := filepath.Join(t.TempDir(), "allow.txt")
		_, err := writeRawMessageAllowlist(path, cp)
		require.NoError(t, err)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		return string(data)
	}

	first := render(uses)

	// Reversed input must produce byte-identical output (stable sort).
	reversed := []rawUse{uses[2], uses[0], uses[1]}
	require.Equal(t, first, render(reversed))

	// Keys appear sorted by group then key.
	idxAlphaA := strings.Index(first, "Alpha/a")
	idxAlphaB := strings.Index(first, "Alpha/b")
	idxZeta := strings.Index(first, "Zeta/z")
	require.Less(t, idxAlphaA, idxAlphaB, "keys sorted within group")
	require.Less(t, idxAlphaB, idxZeta, "groups sorted (a_group before z_group)")
}

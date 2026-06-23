// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTypeRegistryRegister(t *testing.T) {
	t.Parallel()

	// Steps are applied in order against one shared registry; each asserts the
	// register() return and the resulting collision state. The sequence pins:
	// fresh register, duplicate-ref dedup (no collision), name collision,
	// distinct dropped ref on the same name (recorded separately -> dedup keys
	// on the dropped ref, not the name), and a repeated dropped ref (deduped).
	steps := []struct {
		name           string
		input          *goType
		wantOK         bool
		wantGotIsInput bool // when !wantOK, whether got is the existing type (true) or nil (false)
		wantCollisions []nameCollision
	}{
		{
			name:           "fresh register succeeds",
			input:          &goType{Name: "ShardStatistics", SchemaRef: "_common___ShardStatistics", IsShared: true},
			wantOK:         true,
			wantCollisions: nil,
		},
		{
			name:           "duplicate ref returns existing, no collision",
			input:          &goType{Name: "ShardStatistics", SchemaRef: "_common___ShardStatistics"},
			wantOK:         false,
			wantGotIsInput: true,
			wantCollisions: nil,
		},
		{
			name:   "name collision with different ref is dropped and recorded",
			input:  &goType{Name: "ShardStatistics", SchemaRef: "other___ShardStatistics"},
			wantOK: false,
			wantCollisions: []nameCollision{
				{Name: "ShardStatistics", KeptRef: "_common___ShardStatistics", DroppedRef: "other___ShardStatistics"},
			},
		},
		{
			// Guards against regressing the dedup key to t.Name, which would
			// wrongly collapse distinct lost types that share a name.
			name:   "different dropped ref, same name, records separately",
			input:  &goType{Name: "ShardStatistics", SchemaRef: "third___ShardStatistics"},
			wantOK: false,
			wantCollisions: []nameCollision{
				{Name: "ShardStatistics", KeptRef: "_common___ShardStatistics", DroppedRef: "other___ShardStatistics"},
				{Name: "ShardStatistics", KeptRef: "_common___ShardStatistics", DroppedRef: "third___ShardStatistics"},
			},
		},
		{
			name:   "repeated dropped ref does not add a second entry",
			input:  &goType{Name: "ShardStatistics", SchemaRef: "other___ShardStatistics"},
			wantOK: false,
			wantCollisions: []nameCollision{
				{Name: "ShardStatistics", KeptRef: "_common___ShardStatistics", DroppedRef: "other___ShardStatistics"},
				{Name: "ShardStatistics", KeptRef: "_common___ShardStatistics", DroppedRef: "third___ShardStatistics"},
			},
		},
	}

	r := newTypeRegistry(opensearchAPIPkgName)
	var firstKept *goType
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			got, ok := r.register(step.input)
			require.Equal(t, step.wantOK, ok)
			switch {
			case step.wantOK:
				require.Equal(t, step.input, got)
				firstKept = step.input
			case step.wantGotIsInput:
				require.Equal(t, firstKept, got)
			default:
				require.Nil(t, got)
			}
			require.Equal(t, step.wantCollisions, r.collisions)
		})
	}
}

func TestTypeRegistryCheckCollisions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		registrations []*goType
		wantCount     int
		wantContains  []string // substrings the report must contain; empty means no report
	}{
		{
			name:          "no collisions reports zero",
			registrations: []*goType{{Name: "A", SchemaRef: "x___A"}},
			wantCount:     0,
		},
		{
			name: "collision is counted and reported",
			registrations: []*goType{
				{Name: "Dup", SchemaRef: "x___Dup"},
				{Name: "Dup", SchemaRef: "y___Other"},
			},
			wantCount:    1,
			wantContains: []string{"WARNING", `name "Dup"`, "x___Dup", "y___Other"},
		},
		{
			name: "multiple collisions all counted and listed",
			registrations: []*goType{
				{Name: "A", SchemaRef: "p___A"},
				{Name: "A", SchemaRef: "q___A"}, // collision 1
				{Name: "B", SchemaRef: "p___B"},
				{Name: "B", SchemaRef: "q___B"}, // collision 2
			},
			wantCount:    2,
			wantContains: []string{"2 Go type name collision", "p___A", "q___A", "p___B", "q___B"},
		},
		{
			// The same dropped ref re-attempted (e.g. reached via two parent
			// fields) is recorded once, so the count reflects distinct lost types.
			name: "repeated dropped ref is deduplicated",
			registrations: []*goType{
				{Name: "Dup", SchemaRef: "x___Dup"},
				{Name: "Dup", SchemaRef: "y___Other"},
				{Name: "Dup", SchemaRef: "y___Other"},
			},
			wantCount:    1,
			wantContains: []string{`name "Dup"`, "y___Other"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newTypeRegistry(opensearchAPIPkgName)
			for _, gt := range tt.registrations {
				r.register(gt)
			}
			var sb strings.Builder
			require.Equal(t, tt.wantCount, r.checkCollisions(&sb))
			out := sb.String()
			if len(tt.wantContains) == 0 {
				require.Empty(t, out)
			}
			for _, sub := range tt.wantContains {
				require.Contains(t, out, sub)
			}
		})
	}
}

func TestReportCollisions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		registrations []*goType
		wantContains  []string // substrings the output must contain; empty means no output
	}{
		{
			name:          "no collisions writes nothing",
			registrations: []*goType{{Name: "A", SchemaRef: "x___A"}},
		},
		{
			name: "collisions write report plus continuation note",
			registrations: []*goType{
				{Name: "Dup", SchemaRef: "x___Dup"},
				{Name: "Dup", SchemaRef: "y___Other"},
			},
			wantContains: []string{
				"WARNING",               // the checkCollisions report
				"continuing despite",    // the non-fatal note
				"1 type name collision", // deduped count
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newTypeRegistry(opensearchAPIPkgName)
			for _, gt := range tt.registrations {
				r.register(gt)
			}
			var sb strings.Builder
			reportCollisions(&sb, r)
			out := sb.String()
			if len(tt.wantContains) == 0 {
				require.Empty(t, out)
			}
			for _, sub := range tt.wantContains {
				require.Contains(t, out, sub)
			}
		})
	}
}

func TestTypeRegistryLookup(t *testing.T) {
	t.Parallel()

	r := newTypeRegistry(opensearchAPIPkgName)
	t1 := &goType{Name: "ErrorCause", SchemaRef: "_common___ErrorCause", IsShared: true}
	r.register(t1)

	got, ok := r.lookup("_common___ErrorCause")
	require.True(t, ok)
	require.Equal(t, t1, got)

	_, ok = r.lookup("nonexistent")
	require.False(t, ok)

	got, ok = r.lookupByName("ErrorCause")
	require.True(t, ok)
	require.Equal(t, t1, got)
}

func TestTypeRegistryAll(t *testing.T) {
	t.Parallel()

	r := newTypeRegistry(opensearchAPIPkgName)
	t1 := &goType{Name: "A", SchemaRef: "ref_a"}
	t2 := &goType{Name: "B", SchemaRef: "ref_b"}
	t3 := &goType{Name: "C", SchemaRef: "ref_c"}
	r.register(t1)
	r.register(t2)
	r.register(t3)

	all := r.all()
	require.Len(t, all, 3)
	require.Equal(t, "A", all[0].Name)
	require.Equal(t, "B", all[1].Name)
	require.Equal(t, "C", all[2].Name)
}

func TestTypeRegistryShared(t *testing.T) {
	t.Parallel()

	r := newTypeRegistry(opensearchAPIPkgName)
	r.register(&goType{Name: "Zebra", SchemaRef: "ref_z", IsShared: true})
	r.register(&goType{Name: "Alpha", SchemaRef: "ref_a", IsShared: true})
	r.register(&goType{Name: "OpSpecific", SchemaRef: "cluster.health___Something"})

	shared := r.shared()
	require.Len(t, shared, 2)
	require.Equal(t, "Alpha", shared[0].Name)
	require.Equal(t, "Zebra", shared[1].Name)
}

func TestTypeRegistryForOperation(t *testing.T) {
	t.Parallel()

	r := newTypeRegistry(opensearchAPIPkgName)
	r.register(&goType{Name: "ShardStatistics", SchemaRef: "_common___ShardStatistics", IsShared: true})
	r.register(&goType{Name: "ClusterHealthIndexStats", SchemaRef: "cluster.health___IndexHealthStats"})
	r.register(&goType{Name: "ClusterHealthResp", SchemaRef: "cluster.health___HealthResponseBody"})
	r.register(&goType{Name: "IndicesRefreshResp", SchemaRef: "indices.refresh___RefreshResponseBody"})

	opTypes := r.forOperation("cluster.health")
	require.Len(t, opTypes, 2)
	require.Equal(t, "ClusterHealthIndexStats", opTypes[0].Name)
	require.Equal(t, "ClusterHealthResp", opTypes[1].Name)
}

func TestSchemaGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  string
		want string
	}{
		{name: "dotted group", ref: "cluster.health___IndexHealthStats", want: "cluster.health"},
		{name: "common", ref: "_common___ShardStatistics", want: "_common"},
		{name: "no separator", ref: "something", want: ""},
		{name: "group._common", ref: "nodes._common___NodesResponseBase", want: "nodes._common"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, schemaGroup(tt.ref))
		})
	}
}

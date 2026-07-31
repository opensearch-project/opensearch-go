// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// The generated unions over primitive branches (string, []string, int64, and
// the map forms) all share one mechanical surface: Type(), RawJSON(), SetRaw(),
// a New<Union>From<Branch> constructor and a matching accessor per branch, plus
// UnmarshalJSON and MarshalJSON. There are 54 of them across opensearchapi and
// nothing exercised that surface, so a defect in the emitted template would have
// shipped silently.
//
// Rather than hand-write 54 near-identical tests, these drive the surface
// through reflection over a representative sample, asserting the contract the
// template promises: a decoded branch is reported by Type() and returned by its
// own accessor, every other accessor reports a *UnionBranchError naming the
// branch that is set, and a decode round-trips back to equivalent JSON.

// primitiveUnionCase names a union and a wire value for each of its branches.
type primitiveUnionCase struct {
	// value is a zero value of the union type, obtained through a constructor
	// so the test does not need the unexported fields.
	newUnion func() any
	// branches maps a branch accessor name to JSON that decodes as that branch.
	branches map[string]string
}

func primitiveUnionCases() map[string]primitiveUnionCase {
	return map[string]primitiveUnionCase{
		"IndicesUpdateAliasesAddActionAliases": {
			newUnion: func() any { return new(opensearchapi.IndicesUpdateAliasesAddActionAliases) },
			branches: map[string]string{
				"String": `"logs-write"`,
				"Array":  `["logs-write","logs-read"]`,
			},
		},
		"IndicesUpdateAliasesRemoveActionAliases": {
			newUnion: func() any { return new(opensearchapi.IndicesUpdateAliasesRemoveActionAliases) },
			branches: map[string]string{
				"String": `"stale-alias"`,
				"Array":  `["stale-alias","older-alias"]`,
			},
		},
		"StringOrStringArray": {
			newUnion: func() any { return new(opensearchapi.StringOrStringArray) },
			branches: map[string]string{
				"String": `"single"`,
				"Array":  `["one","two"]`,
			},
		},
		"ClusterAllocationExplainClusterInfoShardSizesValue": {
			newUnion: func() any {
				return new(opensearchapi.ClusterAllocationExplainClusterInfoShardSizesValue)
			},
			branches: map[string]string{
				"Int64":  `4096`,
				"String": `"4096"`,
			},
		},
		"NodesInfoNodeTotalIndexingBuffer": {
			newUnion: func() any { return new(opensearchapi.NodesInfoNodeTotalIndexingBuffer) },
			branches: map[string]string{
				"Int64":  `104857600`,
				"String": `"100mb"`,
			},
		},
		"IndicesAnalyzeTextTo": {
			newUnion: func() any { return new(opensearchapi.IndicesAnalyzeTextTo) },
			branches: map[string]string{
				"String": `"one document"`,
				"Array":  `["first","second"]`,
			},
		},
		"StringifiedEpochTimeUnitMillis": {
			newUnion: func() any { return new(opensearchapi.StringifiedEpochTimeUnitMillis) },
			branches: map[string]string{
				"Int64":  `1700000000123`,
				"String": `"1700000000123"`,
			},
		},
	}
}

func TestPrimitiveUnionRoundTrip(t *testing.T) {
	t.Parallel()

	for name, tc := range primitiveUnionCases() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for branch, wire := range tc.branches {
				t.Run(branch, func(t *testing.T) {
					t.Parallel()

					u := tc.newUnion()
					require.NoError(t, json.Unmarshal([]byte(wire), u), "decoding %s as %s", wire, branch)

					// Type() must name the branch that decoded. Its String()
					// is the same token as the accessor name.
					typ := reflect.ValueOf(u).MethodByName("Type").Call(nil)[0]
					gotBranch := typ.MethodByName("String").Call(nil)[0].String()
					require.Equal(t, branch, gotBranch, "Type().String() must name the decoded branch")

					// The matching accessor returns the value with no error.
					got := reflect.ValueOf(u).MethodByName(branch).Call(nil)
					require.True(t, got[1].IsNil(), "%s() on its own branch must not error: %v", branch, got[1])

					// RawJSON keeps the bytes it decoded from.
					raw := reflect.ValueOf(u).MethodByName("RawJSON").Call(nil)[0].Bytes()
					require.JSONEq(t, wire, string(raw), "RawJSON must return the decoded bytes")

					// Marshaling returns to equivalent JSON.
					out, err := json.Marshal(u)
					require.NoError(t, err)
					require.JSONEq(t, wire, string(out), "the union must round-trip")

					// Every other branch reports which branch is actually set,
					// rather than handing back a plausible zero value.
					for other := range tc.branches {
						if other == branch {
							continue
						}
						res := reflect.ValueOf(u).MethodByName(other).Call(nil)
						require.False(t, res[1].IsNil(), "%s() must error when %s is set", other, branch)

						err, ok := res[1].Interface().(error)
						require.True(t, ok)
						var ube *opensearchapi.UnionBranchError
						require.ErrorAs(t, err, &ube)
						require.Equal(t, name, ube.Union)
						require.Equal(t, ube.Want, other)
						require.Equal(t, branch, ube.Got, "Got must name the branch that is set")
					}
				})
			}
		})
	}
}

// TestPrimitiveUnionConstructors covers the New<Union>From<Branch> constructors,
// which are the only way to build a union with a typed branch set.
func TestPrimitiveUnionConstructors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		union  any
		branch string
		wire   string
	}{
		{
			name:   "IndicesUpdateAliasesAddActionAliases",
			union:  ptr(opensearchapi.NewIndicesUpdateAliasesAddActionAliasesFromString("logs-write")),
			branch: "String", wire: `"logs-write"`,
		},
		{
			name:   "IndicesUpdateAliasesAddActionAliases",
			union:  ptr(opensearchapi.NewIndicesUpdateAliasesAddActionAliasesFromArray([]string{"a", "b"})),
			branch: "Array", wire: `["a","b"]`,
		},
		{
			name:   "StringOrStringArray",
			union:  ptr(opensearchapi.NewStringOrStringArrayFromString("single")),
			branch: "String", wire: `"single"`,
		},
		{
			name:   "StringifiedEpochTimeUnitMillis",
			union:  ptr(opensearchapi.NewStringifiedEpochTimeUnitMillisFromInt64(1700000000123)),
			branch: "Int64", wire: `1700000000123`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/"+tt.branch, func(t *testing.T) {
			t.Parallel()

			typ := reflect.ValueOf(tt.union).MethodByName("Type").Call(nil)[0]
			require.Equal(t, tt.branch, typ.MethodByName("String").Call(nil)[0].String(),
				"a constructed union must report the branch it was built with")

			got := reflect.ValueOf(tt.union).MethodByName(tt.branch).Call(nil)
			require.True(t, got[1].IsNil(), "the constructed branch must not error")

			// A constructed union has no raw bytes, so MarshalJSON has to encode
			// the typed value instead of echoing raw.
			out, err := json.Marshal(tt.union)
			require.NoError(t, err)
			require.JSONEq(t, tt.wire, string(out))
		})
	}
}

// TestPrimitiveUnionSetRawAndReset covers the SetRaw escape hatch and the state
// reset UnmarshalJSON performs, both of which clear any previously typed branch.
func TestPrimitiveUnionSetRawAndReset(t *testing.T) {
	t.Parallel()

	t.Run("SetRaw clears the typed branch", func(t *testing.T) {
		t.Parallel()

		u := opensearchapi.NewStringOrStringArrayFromString("typed")
		require.Equal(t, opensearchapi.StringOrStringArrayStringType, u.Type())

		u.SetRaw(json.RawMessage(`["raw","bytes"]`))
		require.Equal(t, opensearchapi.StringOrStringArrayUnknownType, u.Type(),
			"SetRaw must forget the typed branch: the bytes are unparsed")

		// MarshalJSON emits the staged bytes verbatim.
		out, err := json.Marshal(&u)
		require.NoError(t, err)
		require.JSONEq(t, `["raw","bytes"]`, string(out))

		// No branch is set, so an accessor reports "unknown" rather than a zero.
		_, err = u.String()
		var ube *opensearchapi.UnionBranchError
		require.ErrorAs(t, err, &ube)
		require.Equal(t, "unknown", ube.Got)
	})

	t.Run("decoding a second value replaces the first", func(t *testing.T) {
		t.Parallel()

		var u opensearchapi.StringOrStringArray
		require.NoError(t, json.Unmarshal([]byte(`"first"`), &u))
		require.Equal(t, opensearchapi.StringOrStringArrayStringType, u.Type())

		require.NoError(t, json.Unmarshal([]byte(`["second"]`), &u))
		require.Equal(t, opensearchapi.StringOrStringArrayArrayType, u.Type(),
			"a re-decode must not leave the previous branch reachable")

		_, err := u.String()
		require.Error(t, err, "the replaced branch must no longer be readable")
	})

	t.Run("null decodes to no branch", func(t *testing.T) {
		t.Parallel()

		var u opensearchapi.StringOrStringArray
		require.NoError(t, json.Unmarshal([]byte(`null`), &u))
		require.Equal(t, opensearchapi.StringOrStringArrayUnknownType, u.Type())
	})

	t.Run("a token matching no branch is an error", func(t *testing.T) {
		t.Parallel()

		var u opensearchapi.StringifiedEpochTimeUnitMillis
		err := json.Unmarshal([]byte(`{"not":"a scalar"}`), &u)
		require.Error(t, err)
		require.Contains(t, err.Error(), "StringifiedEpochTimeUnitMillis",
			"the error should name the union")
	})
}

func ptr[T any](v T) *T { return &v }

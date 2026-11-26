// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// extraKeysAggTypes lists every aggregation type whose generated codec carries an
// Aggregations map. TestExtraKeysAggTypesMatchGenerated keeps it in step with
// types_gen.go.
var extraKeysAggTypes = []struct {
	name     string
	newValue func() any
}{
	{"CommonAggregationsAdjacencyMatrixBucket", newOf[opensearchapi.CommonAggregationsAdjacencyMatrixBucket]},
	{"CommonAggregationsCompositeBucket", newOf[opensearchapi.CommonAggregationsCompositeBucket]},
	{"CommonAggregationsDateHistogramBucket", newOf[opensearchapi.CommonAggregationsDateHistogramBucket]},
	{"CommonAggregationsDoubleTermsBucket", newOf[opensearchapi.CommonAggregationsDoubleTermsBucket]},
	{"CommonAggregationsGeoHashGridBucket", newOf[opensearchapi.CommonAggregationsGeoHashGridBucket]},
	{"CommonAggregationsGeoTileGridBucket", newOf[opensearchapi.CommonAggregationsGeoTileGridBucket]},
	{"CommonAggregationsHistogramBucket", newOf[opensearchapi.CommonAggregationsHistogramBucket]},
	{"CommonAggregationsIPRangeBucket", newOf[opensearchapi.CommonAggregationsIPRangeBucket]},
	{"CommonAggregationsLongRareTermsBucket", newOf[opensearchapi.CommonAggregationsLongRareTermsBucket]},
	{"CommonAggregationsLongTermsBucket", newOf[opensearchapi.CommonAggregationsLongTermsBucket]},
	{"CommonAggregationsMultiBucketBase", newOf[opensearchapi.CommonAggregationsMultiBucketBase]},
	{"CommonAggregationsMultiTermsBucket", newOf[opensearchapi.CommonAggregationsMultiTermsBucket]},
	{"CommonAggregationsRangeBucket", newOf[opensearchapi.CommonAggregationsRangeBucket]},
	{"CommonAggregationsSignificantLongTermsBucket", newOf[opensearchapi.CommonAggregationsSignificantLongTermsBucket]},
	{"CommonAggregationsSignificantStringTermsBucket", newOf[opensearchapi.CommonAggregationsSignificantStringTermsBucket]},
	{"CommonAggregationsSignificantTermsBucketBase", newOf[opensearchapi.CommonAggregationsSignificantTermsBucketBase]},
	{"CommonAggregationsSingleBucketAggregateBase", newOf[opensearchapi.CommonAggregationsSingleBucketAggregateBase]},
	{"CommonAggregationsStringRareTermsBucket", newOf[opensearchapi.CommonAggregationsStringRareTermsBucket]},
	{"CommonAggregationsStringTermsBucket", newOf[opensearchapi.CommonAggregationsStringTermsBucket]},
	{"CommonAggregationsTermsBucketBase", newOf[opensearchapi.CommonAggregationsTermsBucketBase]},
	{"CommonAggregationsUnsignedLongTermsBucket", newOf[opensearchapi.CommonAggregationsUnsignedLongTermsBucket]},
	{"CommonAggregationsVariableWidthHistogramBucket", newOf[opensearchapi.CommonAggregationsVariableWidthHistogramBucket]},
}

// fixedJSONKeys returns the JSON names of the fixed properties of struct type typ,
// following embedded structs, in declaration order.
func fixedJSONKeys(typ reflect.Type) []string {
	var keys []string
	for f := range typ.Fields() {
		tag, hasTag := f.Tag.Lookup("json")
		name, _, _ := strings.Cut(tag, ",")
		switch {
		case tag == "-":
		case f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct:
			keys = append(keys, fixedJSONKeys(f.Type)...)
		case !hasTag || name == "":
			keys = append(keys, f.Name)
		default:
			keys = append(keys, name)
		}
	}
	return keys
}

func aggregationsOf(t *testing.T, v any) map[string]opensearchapi.CommonAggregationsAggregate {
	t.Helper()
	f := reflect.ValueOf(v).Elem().FieldByName("Aggregations")
	require.True(t, f.IsValid(), "%T has no Aggregations field", v)
	m, ok := reflect.TypeAssert[map[string]opensearchapi.CommonAggregationsAggregate](f)
	require.True(t, ok, "%T.Aggregations has unexpected type %s", v, f.Type())
	return m
}

func TestExtraKeysAggTypes(t *testing.T) {
	t.Parallel()

	const inner = `{"value":1.5}`

	for _, tt := range extraKeysAggTypes {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fixed := fixedJSONKeys(reflect.TypeOf(tt.newValue()).Elem())
			require.Contains(t, fixed, "doc_count")

			t.Run("decode", func(t *testing.T) {
				t.Parallel()

				v := tt.newValue()
				require.NoError(t, json.Unmarshal([]byte(`{"doc_count":2,"inner_agg":`+inner+`}`), v))
				aggs := aggregationsOf(t, v)
				require.Len(t, aggs, 1)
				require.Contains(t, aggs, "inner_agg")
				agg := aggs["inner_agg"]
				require.JSONEq(t, inner, string(agg.RawJSON()))
				for _, k := range fixed {
					require.NotContains(t, aggs, k)
				}
				dc := reflect.Indirect(reflect.ValueOf(v).Elem().FieldByName("DocCount"))
				require.True(t, dc.IsValid(), "%T has no DocCount field", v)
				require.EqualValues(t, 2, dc.Int())
			})

			t.Run("round trip", func(t *testing.T) {
				t.Parallel()

				v := tt.newValue()
				require.NoError(t, json.Unmarshal([]byte(`{"doc_count":2,"inner_agg":`+inner+`}`), v))
				out, err := json.Marshal(v)
				require.NoError(t, err)
				var got map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(out, &got))
				require.Contains(t, got, "inner_agg")
				require.Contains(t, got, "doc_count")
				require.JSONEq(t, inner, string(got["inner_agg"]))
			})

			t.Run("colliding key", func(t *testing.T) {
				t.Parallel()

				v := tt.newValue()
				key := strings.ToUpper(fixed[0])
				reflect.ValueOf(v).Elem().FieldByName("Aggregations").Set(
					reflect.ValueOf(map[string]opensearchapi.CommonAggregationsAggregate{key: {}}))
				_, err := json.Marshal(v)
				require.Error(t, err)
				require.Contains(t, err.Error(), key)
			})

			t.Run("case-variant fixed key on decode", func(t *testing.T) {
				t.Parallel()

				v := tt.newValue()
				require.NoError(t, json.Unmarshal([]byte(`{"DOC_COUNT":3,"inner_agg":`+inner+`}`), v))
				aggs := aggregationsOf(t, v)
				require.Len(t, aggs, 1)
				require.Contains(t, aggs, "inner_agg")
				require.NotContains(t, aggs, "DOC_COUNT")
			})
		})
	}
}

func TestExtraKeysAggTypesMatchGenerated(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("types_gen.go")
	require.NoError(t, err)

	re := regexp.MustCompile(`(?m)^func \(v \*(CommonAggregations\w+)\) UnmarshalJSON`)
	want := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		want[m[1]] = true
	}
	have := map[string]bool{}
	for _, tt := range extraKeysAggTypes {
		have[tt.name] = true
	}

	var missing, extra []string
	for n := range want {
		if !have[n] {
			missing = append(missing, n)
		}
	}
	for n := range have {
		if !want[n] {
			extra = append(extra, n)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	require.Empty(t, missing, "generated types missing from extraKeysAggTypes")
	require.Empty(t, extra, "extraKeysAggTypes names with no generated UnmarshalJSON")
}

func newOf[T any]() any { return new(T) }

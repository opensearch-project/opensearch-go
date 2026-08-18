// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// An unset json.RawMessage body field must be omitted, not emitted as an
// explicit null. OpenSearch declares object-typed body fields ValueType.OBJECT
// (START_OBJECT only), so a null is rejected with
// "[UpdateRequest] <field> doesn't support values of type: VALUE_NULL" rather
// than treated as absent.
func TestUpdateBodyOmitsUnsetRawFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body opensearchapi.UpdateBody
		want string
	}{
		{
			name: "empty body emits neither doc nor upsert",
			body: opensearchapi.UpdateBody{},
			want: `{}`,
		},
		{
			name: "doc only omits upsert",
			body: opensearchapi.UpdateBody{Doc: json.RawMessage(`{"title":"updated"}`)},
			want: `{"doc":{"title":"updated"}}`,
		},
		{
			name: "upsert only omits doc",
			body: opensearchapi.UpdateBody{Upsert: json.RawMessage(`{"title":"created"}`)},
			want: `{"upsert":{"title":"created"}}`,
		},
		{
			name: "doc and upsert both emitted",
			body: opensearchapi.UpdateBody{
				Doc:    json.RawMessage(`{"title":"updated"}`),
				Upsert: json.RawMessage(`{"title":"created"}`),
			},
			want: `{"doc":{"title":"updated"},"upsert":{"title":"created"}}`,
		},
		{
			name: "script only omits doc and upsert",
			body: func() opensearchapi.UpdateBody {
				s := opensearchapi.NewScriptFromInline(opensearchapi.NewInlineScriptFromString("ctx._source.count += 1"))
				return opensearchapi.UpdateBody{Script: &s}
			}(),
			want: `{"script":"ctx._source.count += 1"}`,
		},
		{
			// An explicit null is preserved: the caller asked for it, and
			// RawMessage("null") is len 4 so omitempty does not drop it.
			name: "explicit null upsert is preserved",
			body: opensearchapi.UpdateBody{Upsert: json.RawMessage(`null`)},
			want: `{"upsert":null}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(tt.body)
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(got))
		})
	}
}

// QueryContainer is shared by ~15 request bodies (search, count, explain,
// delete_by_query, ...). Every branch it carries must stay absent from the wire
// unless the caller set it: the server rejects a null query clause with
// "[distance_feature] query malformed, no start_object after query name".
func TestQueryContainerOmitsUnsetBranches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		container opensearchapi.CommonQueryDSLQueryContainer
		want      string
	}{
		{
			name:      "pointer branch: match_all",
			container: opensearchapi.CommonQueryDSLQueryContainer{MatchAll: &opensearchapi.CommonQueryDSLQueryBase{}},
			want:      `{"match_all":{}}`,
		},
		{
			name:      "pointer branch: match_none",
			container: opensearchapi.CommonQueryDSLQueryContainer{MatchNone: &opensearchapi.CommonQueryDSLQueryBase{}},
			want:      `{"match_none":{}}`,
		},
		{
			name: "map branch, shorthand form: match_phrase",
			container: opensearchapi.CommonQueryDSLQueryContainer{
				MatchPhrase: map[string]opensearchapi.CommonQueryDSLMatchPhraseQuery{
					"title": opensearchapi.NewCommonQueryDSLMatchPhraseQueryFromString("hello"),
				},
			},
			want: `{"match_phrase":{"title":"hello"}}`,
		},
		{
			// The full form of a field-scoped query, which carries the clause's
			// options alongside the term.
			name: "map branch, full form: match",
			container: func() opensearchapi.CommonQueryDSLQueryContainer {
				query := opensearchapi.NewFieldValueFromString("hello")
				operator := "and"
				return opensearchapi.CommonQueryDSLQueryContainer{
					Match: map[string]opensearchapi.CommonQueryDSLMatchQuery{
						"title": opensearchapi.NewCommonQueryDSLMatchQueryFromObject1(
							opensearchapi.CommonQueryDSLMatchQueryObject1{Query: &query, Operator: &operator},
						),
					},
				}
			}(),
			want: `{"match":{"title":{"query":"hello","operator":"and"}}}`,
		},
		{
			name: "union branch set is emitted: distance_feature",
			container: func() opensearchapi.CommonQueryDSLQueryContainer {
				q := opensearchapi.NewCommonQueryDSLDistanceFeatureQueryFromObject0(
					opensearchapi.CommonQueryDSLDistanceFeatureQueryObject0{
						Field:  "location",
						Origin: opensearchapi.NewGeoLocationFromString("52.37,4.89"),
						Pivot:  "1km",
					},
				)
				return opensearchapi.CommonQueryDSLQueryContainer{DistanceFeature: &q}
			}(),
			want: `{"distance_feature":{"field":"location","origin":"52.37,4.89","pivot":"1km"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tt.container)
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(got))

			// The container is reached through a request body in practice, so
			// assert the nested form leaks no null either.
			body, err := json.Marshal(opensearchapi.SearchBody{Query: &tt.container})
			require.NoError(t, err)
			require.JSONEq(t, `{"query":`+tt.want+`}`, string(body))
			require.NotContains(t, string(body), "null")
		})
	}
}

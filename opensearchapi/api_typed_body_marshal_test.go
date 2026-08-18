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
						"title": opensearchapi.NewCommonQueryDSLMatchQueryFromQuery(
							opensearchapi.CommonQueryDSLMatchQueryQuery{Query: &query, Operator: &operator},
						),
					},
				}
			}(),
			want: `{"match":{"title":{"query":"hello","operator":"and"}}}`,
		},
		{
			// Every remaining field-scoped clause the CHANGELOG names, in its
			// shorthand form. Each must marshal to the bare value it did before the
			// clause became a union, which is what "no wire-format change for
			// equivalent code" means.
			name: "map branches, shorthand form: the remaining clauses",
			container: opensearchapi.CommonQueryDSLQueryContainer{
				Term: map[string]opensearchapi.CommonQueryDSLTermQuery{
					"a": opensearchapi.NewCommonQueryDSLTermQueryFromFieldValue(opensearchapi.NewFieldValueFromString("t")),
				},
				Fuzzy: map[string]opensearchapi.CommonQueryDSLFuzzyQuery{
					"b": opensearchapi.NewCommonQueryDSLFuzzyQueryFromFieldValue(opensearchapi.NewFieldValueFromString("f")),
				},
				Prefix: map[string]opensearchapi.CommonQueryDSLPrefixQuery{
					"c": opensearchapi.NewCommonQueryDSLPrefixQueryFromString("p"),
				},
				Wildcard: map[string]opensearchapi.CommonQueryDSLWildcardQuery{
					"d": opensearchapi.NewCommonQueryDSLWildcardQueryFromString("w*"),
				},
				Regexp: map[string]opensearchapi.CommonQueryDSLRegexpQuery{
					"e": opensearchapi.NewCommonQueryDSLRegexpQueryFromString("r.*"),
				},
				SpanTerm: map[string]opensearchapi.CommonQueryDSLSpanTermQuery{
					"f": opensearchapi.NewCommonQueryDSLSpanTermQueryFromString("s"),
				},
				Common: map[string]opensearchapi.CommonQueryDSLCommonTermsQuery{
					"g": opensearchapi.NewCommonQueryDSLCommonTermsQueryFromString("c"),
				},
				MatchBoolPrefix: map[string]opensearchapi.CommonQueryDSLMatchBoolPrefixQuery{
					"h": opensearchapi.NewCommonQueryDSLMatchBoolPrefixQueryFromString("mbp"),
				},
				MatchPhrasePrefix: map[string]opensearchapi.CommonQueryDSLMatchPhrasePrefixQuery{
					"i": opensearchapi.NewCommonQueryDSLMatchPhrasePrefixQueryFromString("mpp"),
				},
			},
			want: `{"term":{"a":"t"},"fuzzy":{"b":"f"},"prefix":{"c":"p"},"wildcard":{"d":"w*"},` +
				`"regexp":{"e":"r.*"},"span_term":{"f":"s"},"common":{"g":"c"},` +
				`"match_bool_prefix":{"h":"mbp"},"match_phrase_prefix":{"i":"mpp"}}`,
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

// Both distance_feature forms marshal, but only the geo form is reachable when
// decoding one: the spec declares the same required keys (field, origin, pivot)
// on both, and they differ only in leaf types a JSON key probe cannot see, since
// Origin accepts a bare string and both Pivot types are strings. Type() therefore
// reports Object0 whichever form arrives, which UPGRADING_V5.md documents and
// this pins. Generation reports the collision rather than dropping the date
// branch, because that branch is still the one to construct when sending it.
func TestDistanceFeatureDecodesAsGeoForm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "geo form", body: `{"field":"location","origin":"52.37,4.89","pivot":"1km"}`},
		{name: "date form", body: `{"field":"@timestamp","origin":"2024-01-01","pivot":"7d"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var q opensearchapi.CommonQueryDSLDistanceFeatureQuery
			require.NoError(t, json.Unmarshal([]byte(tt.body), &q))
			require.Equal(t, opensearchapi.CommonQueryDSLDistanceFeatureQueryObject0Type, q.Type())

			// The raw bytes are retained, so a caller that needs the form as sent
			// reads them rather than the branch.
			require.JSONEq(t, tt.body, string(q.RawJSON()))
		})
	}
}

// A clause union built without one of its From* constructors carries no branch, so
// it marshals as null: the same shape #1066 reported, reachable from the caller's
// side instead of the generator's. UPGRADING_V5.md documents it. This pins the
// current behavior so that making an unset union marshal to an error is a
// deliberate, visible change rather than a silent one.
func TestUnsetClauseUnionMarshalsNull(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		container opensearchapi.CommonQueryDSLQueryContainer
		want      string
	}{
		{
			name: "zero value as a map entry",
			container: opensearchapi.CommonQueryDSLQueryContainer{
				MatchPhrase: map[string]opensearchapi.CommonQueryDSLMatchPhraseQuery{"title": {}},
			},
			want: `{"match_phrase":{"title":null}}`,
		},
		{
			name: "zero value behind a pointer field",
			container: opensearchapi.CommonQueryDSLQueryContainer{
				DistanceFeature: &opensearchapi.CommonQueryDSLDistanceFeatureQuery{},
			},
			want: `{"distance_feature":null}`,
		},
		{
			// A nil map is absent, which is the case the reported bug was about.
			name:      "nil map is omitted",
			container: opensearchapi.CommonQueryDSLQueryContainer{},
			want:      `{}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(tt.container)
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(got))
		})
	}
}

// Script and ScriptsPainlessExecuteBody.Script are two different migrations that
// UPGRADING_V5.md documents together: an update body takes the Script union, while
// painless takes the InlineScript union directly. Both snippets are exercised here
// because the guides are not compiled by CI.
func TestScriptBodiesMarshalDocumentedShapes(t *testing.T) {
	t.Parallel()

	src := "ctx._source.count += 1"

	script := opensearchapi.NewScriptFromInline(opensearchapi.NewInlineScriptFromString(src))
	update, err := json.Marshal(opensearchapi.UpdateBody{Script: &script})
	require.NoError(t, err)
	require.JSONEq(t, `{"script":"`+src+`"}`, string(update))

	inline := opensearchapi.NewInlineScriptFromString(src)
	painless, err := json.Marshal(opensearchapi.ScriptsPainlessExecuteBody{Script: &inline})
	require.NoError(t, err)
	require.JSONEq(t, `{"script":"`+src+`"}`, string(painless))
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration

package opensearchapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
)

// The typed UpdateBody is the generated user surface, distinct from the
// BodyReader escape hatch every other update test uses. Each case here 400'd
// before json.RawMessage fields honoured omitempty, because an unset Doc or
// Upsert marshalled to an explicit null.
func TestManual_UpdateTypedBody(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-update-typed-body")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	script := opensearchapi.NewScriptFromInline(opensearchapi.NewInlineScriptFromString("ctx._source.count += 1"))

	tests := []struct {
		name       string
		id         string
		seed       bool
		body       opensearchapi.UpdateBody
		wantResult opensearchapi.Result
	}{
		{
			name:       "doc only",
			id:         "doc-only",
			seed:       true,
			body:       opensearchapi.UpdateBody{Doc: json.RawMessage(`{"title":"Updated"}`)},
			wantResult: opensearchapi.ResultUpdated,
		},
		{
			name:       "script only",
			id:         "script-only",
			seed:       true,
			body:       opensearchapi.UpdateBody{Script: &script},
			wantResult: opensearchapi.ResultUpdated,
		},
		{
			// Upsert is only meaningful alongside doc or script - the server
			// rejects an upsert-only body with "script or doc is missing" - so
			// this exercises the upsert path against a document that is absent.
			name: "doc and upsert creates a missing document",
			id:   "upsert-creates",
			seed: false,
			body: opensearchapi.UpdateBody{
				Doc:    json.RawMessage(`{"title":"Updated"}`),
				Upsert: json.RawMessage(`{"title":"Created","count":1}`),
			},
			wantResult: opensearchapi.ResultCreated,
		},
		{
			name: "doc and upsert updates an existing document",
			id:   "doc-and-upsert",
			seed: true,
			body: opensearchapi.UpdateBody{
				Doc:    json.RawMessage(`{"title":"Updated"}`),
				Upsert: json.RawMessage(`{"title":"Created","count":1}`),
			},
			wantResult: opensearchapi.ResultUpdated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.seed {
				_, err := client.Doc.Index(t.Context(), opensearchapi.IndexReq{
					Index:  index,
					ID:     tt.id,
					Body:   strings.NewReader(`{"title":"Original","count":1}`),
					Params: &opensearchapi.IndexParams{Refresh: "true"},
				})
				require.NoError(t, err)
			}

			resp, err := client.Doc.Update(t.Context(), opensearchapi.UpdateReq{
				Index: index,
				ID:    tt.id,
				Body:  &tt.body,
			})
			require.NoError(t, err)
			require.Equal(t, tt.wantResult, resp.Result)
			require.Equal(t, tt.id, resp.ID)
		})
	}
}

// A typed SearchBody carries the shared QueryContainer, whose distance_feature
// branch previously leaked a null into every query and made the typed search
// path unusable. Its field-scoped query clauses accept both the shorthand and
// the full form, so the cases below send each against a live cluster.
func TestManual_SearchTypedBody(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-search-typed-body")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	_, err = client.Doc.Index(t.Context(), opensearchapi.IndexReq{
		Index:  index,
		ID:     "doc-1",
		Body:   strings.NewReader(`{"title":"hello"}`),
		Params: &opensearchapi.IndexParams{Refresh: "true"},
	})
	require.NoError(t, err)

	tests := []struct {
		name      string
		container opensearchapi.CommonQueryDSLQueryContainer
	}{
		{
			name:      "match_all",
			container: opensearchapi.CommonQueryDSLQueryContainer{MatchAll: &opensearchapi.CommonQueryDSLQueryBase{}},
		},
		{
			name: "match_phrase shorthand",
			container: opensearchapi.CommonQueryDSLQueryContainer{
				MatchPhrase: map[string]opensearchapi.CommonQueryDSLMatchPhraseQuery{
					"title": opensearchapi.NewCommonQueryDSLMatchPhraseQueryFromString("hello"),
				},
			},
		},
		{
			name: "match full form",
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
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Search(t.Context(), &opensearchapi.SearchReq{
				Indices: []string{index},
				Body:    &opensearchapi.SearchBody{Query: &tt.container},
			})
			require.NoError(t, err)
			require.Equal(t, 1, len(resp.Hits.Hits))
		})
	}
}

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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v5/opensearchapi/internal/osapitest"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
)

func TestManual_Cat(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-cat")
	alias := testutil.MustUniqueString(t, "alias-cat")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	_, err = client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
	require.NoError(t, err)
	_, err = client.Indices.PutAlias(t.Context(), opensearchapi.IndicesPutAliasReq{
		Indices: []string{index},
		Name:    alias,
	})
	require.NoError(t, err)
	_, err = client.Doc.Index(t.Context(), opensearchapi.IndexReq{
		Index:  index,
		ID:     "1",
		Body:   strings.NewReader(`{"title":"cat-test"}`),
		Params: &opensearchapi.IndexParams{Refresh: "true"},
	})
	require.NoError(t, err)

	tests := []struct {
		name string
		call func(ctx context.Context) (records int, inspect opensearchapi.Inspect, err error)
	}{
		{
			name: "health",
			call: func(ctx context.Context) (int, opensearchapi.Inspect, error) {
				resp, err := client.Cat.Health(ctx, &opensearchapi.CatHealthReq{
					Params: &opensearchapi.CatHealthParams{DebugParams: opensearchapi.DebugParams{Format: "json"}},
				})
				if err != nil {
					return 0, opensearchapi.Inspect{}, err
				}
				testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
				return len(resp.Records), resp.Inspect(), nil
			},
		},
		{
			name: "indices",
			call: func(ctx context.Context) (int, opensearchapi.Inspect, error) {
				resp, err := client.Cat.Indices(ctx, &opensearchapi.CatIndicesReq{
					Indices: []string{index},
					Params:  &opensearchapi.CatIndicesParams{DebugParams: opensearchapi.DebugParams{Format: "json"}},
				})
				if err != nil {
					return 0, opensearchapi.Inspect{}, err
				}
				testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
				return len(resp.Records), resp.Inspect(), nil
			},
		},
		{
			name: "aliases",
			call: func(ctx context.Context) (int, opensearchapi.Inspect, error) {
				resp, err := client.Cat.Aliases(ctx, &opensearchapi.CatAliasesReq{
					Name:   []string{alias},
					Params: &opensearchapi.CatAliasesParams{DebugParams: opensearchapi.DebugParams{Format: "json"}},
				})
				if err != nil {
					return 0, opensearchapi.Inspect{}, err
				}
				testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
				return len(resp.Records), resp.Inspect(), nil
			},
		},
		{
			name: "shards",
			call: func(ctx context.Context) (int, opensearchapi.Inspect, error) {
				resp, err := client.Cat.Shards(ctx, &opensearchapi.CatShardsReq{
					Params: &opensearchapi.CatShardsParams{DebugParams: opensearchapi.DebugParams{Format: "json"}},
				})
				if err != nil {
					return 0, opensearchapi.Inspect{}, err
				}
				testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
				return len(resp.Records), resp.Inspect(), nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records, inspect, err := tt.call(t.Context())
			require.NoError(t, err)
			require.Positive(t, records)
			require.NotNil(t, inspect.Response)
		})
	}

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Cat.Health(t.Context(), nil)
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration

package osapi_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/osapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/osapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/osapi/testutil"
)

func TestManual_Cat(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-cat")
	alias := testutil.MustUniqueString(t, "alias-cat")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &osapi.IndicesDeleteReq{Index: []string{index}})
	})

	_, err = client.Indices.Create(t.Context(), osapi.IndicesCreateReq{Index: index})
	require.NoError(t, err)
	_, err = client.Indices.PutAlias(t.Context(), osapi.IndicesPutAliasReq{
		Index: []string{index},
		Name:  alias,
	})
	require.NoError(t, err)
	_, err = client.Index(t.Context(), osapi.IndexReq{
		Index:  index,
		ID:     "1",
		Body:   strings.NewReader(`{"title":"cat-test"}`),
		Params: &osapi.IndexParams{Refresh: "true"},
	})
	require.NoError(t, err)

	tests := []struct {
		name string
		call func(ctx context.Context) (records int, inspect osapi.Inspect, err error)
	}{
		{
			name: "health",
			call: func(ctx context.Context) (int, osapi.Inspect, error) {
				resp, err := client.Cat.Health(ctx, &osapi.CatHealthReq{
					Params: &osapi.CatHealthParams{DebugParams: osapi.DebugParams{Format: "json"}},
				})
				if err != nil {
					return 0, osapi.Inspect{}, err
				}
				testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
				return len(resp.Records), resp.Inspect(), nil
			},
		},
		{
			name: "indices",
			call: func(ctx context.Context) (int, osapi.Inspect, error) {
				resp, err := client.Cat.Indices(ctx, &osapi.CatIndicesReq{
					Index:  []string{index},
					Params: &osapi.CatIndicesParams{DebugParams: osapi.DebugParams{Format: "json"}},
				})
				if err != nil {
					return 0, osapi.Inspect{}, err
				}
				testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
				return len(resp.Records), resp.Inspect(), nil
			},
		},
		{
			name: "aliases",
			call: func(ctx context.Context) (int, osapi.Inspect, error) {
				resp, err := client.Cat.Aliases(ctx, &osapi.CatAliasesReq{
					Name:   []string{alias},
					Params: &osapi.CatAliasesParams{DebugParams: osapi.DebugParams{Format: "json"}},
				})
				if err != nil {
					return 0, osapi.Inspect{}, err
				}
				testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
				return len(resp.Records), resp.Inspect(), nil
			},
		},
		{
			name: "shards",
			call: func(ctx context.Context) (int, osapi.Inspect, error) {
				resp, err := client.Cat.Shards(ctx, &osapi.CatShardsReq{
					Params: &osapi.CatShardsParams{DebugParams: osapi.DebugParams{Format: "json"}},
				})
				if err != nil {
					return 0, osapi.Inspect{}, err
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

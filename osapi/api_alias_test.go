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
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/osapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/osapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/osapi/testutil"
)

func TestManual_IndicesAlias(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-alias")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &osapi.IndicesDeleteReq{Index: []string{index}})
	})

	_, err = client.Indices.Create(t.Context(), osapi.IndicesCreateReq{Index: index})
	require.NoError(t, err)

	tests := []struct {
		name  string
		alias string
	}{
		{name: "alias-one", alias: testutil.MustUniqueString(t, "alias-one")},
		{name: "alias-two", alias: testutil.MustUniqueString(t, "alias-two")},
	}

	for _, tt := range tests {
		t.Run("put/"+tt.name, func(t *testing.T) {
			resp, err := client.Indices.PutAlias(t.Context(), osapi.IndicesPutAliasReq{
				Index: []string{index},
				Name:  tt.alias,
			})
			require.NoError(t, err)
			require.True(t, resp.Acknowledged)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	}

	for _, tt := range tests {
		t.Run("get/"+tt.name, func(t *testing.T) {
			resp, err := client.Indices.GetAlias(t.Context(), &osapi.IndicesGetAliasReq{
				Index: []string{index},
				Name:  []string{tt.alias},
			})
			require.NoError(t, err)
			require.Contains(t, resp.Entries, index)
			require.Contains(t, resp.Entries[index].Aliases, tt.alias)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	}

	for _, tt := range tests {
		t.Run("exists/"+tt.name, func(t *testing.T) {
			resp, err := client.Indices.ExistsAlias(t.Context(), &osapi.IndicesExistsAliasReq{
				Index: []string{index},
				Name:  []string{tt.alias},
			})
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}

	for _, tt := range tests {
		t.Run("delete/"+tt.name, func(t *testing.T) {
			resp, err := client.Indices.DeleteAlias(t.Context(), &osapi.IndicesDeleteAliasReq{
				Index: []string{index},
				Name:  []string{tt.alias},
			})
			require.NoError(t, err)
			require.True(t, resp.Acknowledged)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	}

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Indices.GetAlias(t.Context(), nil)
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

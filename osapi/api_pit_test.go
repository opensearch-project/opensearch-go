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
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/osapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/osapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/osapi/testutil"
)

func TestManual_PIT(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	testutil.SkipIfVersion(t, client, "<", "2.4", "PIT")

	index := testutil.MustUniqueString(t, "test-pit")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &osapi.IndicesDeleteReq{Index: []string{index}})
	})

	_, err = client.Index(t.Context(), osapi.IndexReq{
		Index:  index,
		ID:     "1",
		Body:   strings.NewReader(`{"title":"PIT test"}`),
		Params: &osapi.IndexParams{Refresh: "true"},
	})
	require.NoError(t, err)

	tests := []struct {
		name string
	}{
		{name: "create and delete PIT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			createResp, err := client.CreatePIT(t.Context(), &osapi.CreatePITReq{
				Index:  []string{index},
				Params: &osapi.CreatePITParams{KeepAlive: 1 * time.Minute},
			})
			require.NoError(t, err)
			require.NotNil(t, createResp.PITID)
			require.NotEmpty(t, *createResp.PITID)
			testutil.CompareRawJSONwithParsedJSON(t, createResp, createResp.Inspect().Response)

			pitID := *createResp.PITID

			getAllResp, err := client.GetAllPits(t.Context(), nil)
			require.NoError(t, err)
			require.NotEmpty(t, getAllResp.Pits)

			found := false
			for _, pit := range getAllResp.Pits {
				if pit.PITID != nil && *pit.PITID == pitID {
					found = true
					break
				}
			}
			require.True(t, found, "created PIT not found in GetAllPits response")
			testutil.CompareRawJSONwithParsedJSON(t, getAllResp, getAllResp.Inspect().Response)

			deleteResp, err := client.DeletePIT(t.Context(), &osapi.DeletePITReq{
				Body: &osapi.DeletePITBody{PITID: []string{pitID}},
			})
			require.NoError(t, err)
			require.NotEmpty(t, deleteResp.Pits)
			require.NotNil(t, deleteResp.Pits[0].Successful)
			require.True(t, *deleteResp.Pits[0].Successful)
			testutil.CompareRawJSONwithParsedJSON(t, deleteResp, deleteResp.Inspect().Response)
		})
	}

	t.Run("delete all pits", func(t *testing.T) {
		_, err := client.CreatePIT(t.Context(), &osapi.CreatePITReq{
			Index:  []string{index},
			Params: &osapi.CreatePITParams{KeepAlive: 1 * time.Minute},
		})
		require.NoError(t, err)

		resp, err := client.DeleteAllPits(t.Context(), nil)
		require.NoError(t, err)
		require.NotEmpty(t, resp.Pits)
		testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.GetAllPits(t.Context(), nil)
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

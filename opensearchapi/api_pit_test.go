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
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v5/opensearchapi/internal/osapitest"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
)

func TestManual_PIT(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	testutil.SkipIfVersion(t, client, "<", "2.4", "PIT")

	index := testutil.MustUniqueString(t, "test-pit")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	_, err = client.Doc.Index(t.Context(), opensearchapi.IndexReq{
		Index:  index,
		ID:     "1",
		Body:   strings.NewReader(`{"title":"PIT test"}`),
		Params: &opensearchapi.IndexParams{Refresh: "true"},
	})
	require.NoError(t, err)

	tests := []struct {
		name string
	}{
		{name: "create and delete PIT"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			createResp, err := client.PIT.Create(t.Context(), &opensearchapi.CreatePITReq{
				Indices: []string{index},
				Params:  &opensearchapi.CreatePITParams{KeepAlive: 1 * time.Minute},
			})
			require.NoError(t, err)
			require.NotNil(t, createResp.PITID)
			require.NotEmpty(t, *createResp.PITID)
			testutil.CompareRawJSONwithParsedJSON(t, createResp, createResp.Inspect().Response)

			pitID := *createResp.PITID

			getAllResp, err := client.PIT.GetAll(t.Context(), nil)
			require.NoError(t, err)
			require.NotEmpty(t, getAllResp.PITs)

			found := false
			for _, pit := range getAllResp.PITs {
				if pit.PITID != nil && *pit.PITID == pitID {
					found = true
					break
				}
			}
			require.True(t, found, "created PIT not found in GetAllPits response")
			testutil.CompareRawJSONwithParsedJSON(t, getAllResp, getAllResp.Inspect().Response)

			deleteResp, err := client.PIT.Delete(t.Context(), &opensearchapi.DeletePITReq{
				Body: &opensearchapi.DeletePITBody{PITID: []string{pitID}},
			})
			require.NoError(t, err)
			require.NotEmpty(t, deleteResp.PITs)
			require.NotNil(t, deleteResp.PITs[0].Successful)
			require.True(t, *deleteResp.PITs[0].Successful)
			testutil.CompareRawJSONwithParsedJSON(t, deleteResp, deleteResp.Inspect().Response)
		})
	}

	t.Run("delete all pits", func(t *testing.T) {
		_, err := client.PIT.Create(t.Context(), &opensearchapi.CreatePITReq{
			Indices: []string{index},
			Params:  &opensearchapi.CreatePITParams{KeepAlive: 1 * time.Minute},
		})
		require.NoError(t, err)

		resp, err := client.PIT.DeleteAll(t.Context(), nil)
		require.NoError(t, err)
		require.NotEmpty(t, resp.PITs)
		testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.PIT.GetAll(t.Context(), nil)
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

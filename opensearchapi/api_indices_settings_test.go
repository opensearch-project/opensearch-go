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

func TestManual_IndicesSettings(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-settings")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	_, err = client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
	require.NoError(t, err)

	t.Run("put and get settings", func(t *testing.T) {
		tests := []struct {
			name     string
			settings string
			checkKey string
		}{
			{
				name:     "set refresh interval",
				settings: `{"index":{"refresh_interval":"5s"}}`,
				checkKey: index,
			},
			{
				name:     "set number of replicas",
				settings: `{"index":{"number_of_replicas":"0"}}`,
				checkKey: index,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				putResp, err := client.Indices.PutSettings(t.Context(), &opensearchapi.IndicesPutSettingsReq{
					Indices:    []string{index},
					BodyReader: strings.NewReader(tt.settings),
				})
				require.NoError(t, err)
				require.True(t, putResp.Acknowledged)
				testutil.CompareRawJSONwithParsedJSON(t, putResp, putResp.Inspect().Response)

				getResp, err := client.Indices.GetSettings(t.Context(), &opensearchapi.IndicesGetSettingsReq{
					Indices: []string{index},
				})
				require.NoError(t, err)
				require.Contains(t, getResp.Entries, tt.checkKey)
				testutil.CompareRawJSONwithParsedJSON(t, getResp, getResp.Inspect().Response)
			})
		}
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Indices.GetSettings(t.Context(), nil)
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

func TestManual_IndicesMapping(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-mapping")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	_, err = client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
	require.NoError(t, err)

	tests := []struct {
		name    string
		mapping string
	}{
		{
			name:    "add keyword field",
			mapping: `{"properties":{"status":{"type":"keyword"}}}`,
		},
		{
			name:    "add text field",
			mapping: `{"properties":{"description":{"type":"text"}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			putResp, err := client.Indices.PutMapping(t.Context(), &opensearchapi.IndicesPutMappingReq{
				Indices:    []string{index},
				BodyReader: strings.NewReader(tt.mapping),
			})
			require.NoError(t, err)
			require.True(t, putResp.Acknowledged)
			testutil.CompareRawJSONwithParsedJSON(t, putResp, putResp.Inspect().Response)
		})
	}

	t.Run("get mapping", func(t *testing.T) {
		resp, err := client.Indices.GetMapping(t.Context(), &opensearchapi.IndicesGetMappingReq{
			Indices: []string{index},
		})
		require.NoError(t, err)
		require.Contains(t, resp.Entries, index)
		testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Indices.GetMapping(t.Context(), nil)
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

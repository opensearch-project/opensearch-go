// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration

package opensearchapi_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v5/opensearchapi/internal/osapitest"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
)

func TestManual_ClusterHealth(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	tests := []struct {
		name string
		req  *opensearchapi.ClusterHealthReq
	}{
		{name: "nil request", req: nil},
		{name: "empty request", req: &opensearchapi.ClusterHealthReq{}},
		{name: "level indices", req: &opensearchapi.ClusterHealthReq{
			Params: &opensearchapi.ClusterHealthParams{Level: "indices"},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Cluster.Health(t.Context(), tt.req)
			require.NoError(t, err)
			require.NotEmpty(t, resp.ClusterName)
			require.NotEmpty(t, resp.Status)
			require.Positive(t, resp.NumberOfNodes)
			require.Positive(t, resp.NumberOfDataNodes)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	}

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Cluster.Health(t.Context(), nil)
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

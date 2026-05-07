// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration

package osapi_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/osapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/osapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/osapi/testutil"
)

func TestManual_ClusterHealth(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	tests := []struct {
		name string
		req  *osapi.ClusterHealthReq
	}{
		{name: "nil request", req: nil},
		{name: "empty request", req: &osapi.ClusterHealthReq{}},
		{name: "level indices", req: &osapi.ClusterHealthReq{
			Params: &osapi.ClusterHealthParams{Level: "indices"},
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

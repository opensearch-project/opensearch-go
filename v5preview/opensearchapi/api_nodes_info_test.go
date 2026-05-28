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

	osapitest "github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi/internal/osapitest"
	"github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi/testutil"
)

func TestManual_NodesInfo(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	t.Run("with nil request", func(t *testing.T) {
		resp, err := client.Nodes.Info(t.Context(), nil)
		require.NoError(t, err)
		require.NotEmpty(t, resp.ClusterName)
		require.NotEmpty(t, resp.Nodes)
		for _, node := range resp.Nodes {
			require.NotEmpty(t, node.Name)
			require.NotEmpty(t, node.TransportAddress)
			require.NotEmpty(t, node.Roles)
		}
		testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Nodes.Info(t.Context(), nil)
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

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

func TestManual_Info(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	t.Run("with nil request", func(t *testing.T) {
		resp, err := client.Info(t.Context(), nil)
		require.NoError(t, err)
		require.NotEmpty(t, resp.ClusterName)
		require.NotEmpty(t, resp.Version.Number)
		testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("with request", func(t *testing.T) {
		resp, err := client.Info(t.Context(), &osapi.InfoReq{})
		require.NoError(t, err)
		require.NotEmpty(t, resp.ClusterName)
		require.NotEmpty(t, resp.Version.Number)
		testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Info(t.Context(), nil)
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

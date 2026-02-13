// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (plugins || plugin_security)

package security_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
	ossectest "github.com/opensearch-project/opensearch-go/v4/plugins/security/internal/test"
)

func TestSecurityFlushCache(t *testing.T) {
	testutil.SkipIfNotSecure(t)
	client, err := ossectest.NewClient(t)
	require.NoError(t, err)

	failingClient, err := ossectest.CreateFailingClient()
	require.NoError(t, err)

	t.Run("without request", func(t *testing.T) {
		resp, err := client.FlushCache(t.Context(), nil)
		require.NoError(t, err)
		assert.NotNil(t, resp)
		testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		res, err := failingClient.FlushCache(t.Context(), nil)
		require.Error(t, err)
		assert.NotNil(t, res)
		ossectest.VerifyInspect(t, res.Inspect())
	})
}

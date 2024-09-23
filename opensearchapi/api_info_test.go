// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
)

func TestInfo(t *testing.T) {
	client, err := ostest.NewClient()
	require.Nil(t, err)
	t.Run("with nil request", func(t *testing.T) {
		resp, httpResp, err := client.Info(nil, nil)
		require.Nil(t, err)
		assert.NotEmpty(t, resp)
		assert.NotNil(t, httpResp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, httpResp)
	})

	t.Run("with request", func(t *testing.T) {
		resp, httpResp, err := client.Info(nil, &opensearchapi.InfoReq{})
		require.Nil(t, err)
		assert.NotEmpty(t, resp)
		assert.NotNil(t, httpResp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, httpResp)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.Nil(t, err)

		res, httpResp, err := failingClient.Info(nil, nil)
		assert.NotNil(t, err)
		assert.Nil(t, res)
		assert.NotNil(t, httpResp)
		osapitest.VerifyResponse(t, httpResp)
	})
}

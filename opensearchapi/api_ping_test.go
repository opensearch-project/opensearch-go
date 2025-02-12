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

func TestPing(t *testing.T) {
	client, err := ostest.NewClient()
	require.Nil(t, err)

	t.Run("with nil request", func(t *testing.T) {
		resp, err := client.Ping(nil, nil)
		require.Nil(t, err)
		assert.NotEmpty(t, resp)
	})

	t.Run("with request", func(t *testing.T) {
		resp, err := client.Ping(nil, &opensearchapi.PingReq{})
		require.Nil(t, err)
		assert.NotEmpty(t, resp)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.Nil(t, err)

		httpResp, err := failingClient.Ping(nil, nil)
		assert.NotNil(t, err)
		assert.NotNil(t, httpResp)
		osapitest.VerifyResponse(t, httpResp)
	})
}

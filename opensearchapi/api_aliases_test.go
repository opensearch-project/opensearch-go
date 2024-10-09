// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
)

func TestAliases(t *testing.T) {
	t.Run("Aliases", func(t *testing.T) {
		client, err := ostest.NewClient()
		require.Nil(t, err)

		index := "test-aliases"
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{index}})
		})

		_, _, err = client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		t.Run("with request", func(t *testing.T) {
			resp, httpResp, err := client.Aliases(
				nil,
				opensearchapi.AliasesReq{
					Body: strings.NewReader(`{"actions":[{"add":{"index":"test-aliases","alias":"logs"}},{"remove":{"index":"test-aliases","alias":"logs"}}]}`),
				},
			)
			require.Nil(t, err)
			require.NotNil(t, resp)
			require.NotNil(t, httpResp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, httpResp)
		})

		t.Run("with failing request", func(t *testing.T) {
			failingClient, err := osapitest.CreateFailingClient()
			require.Nil(t, err)

			res, httpResp, err := failingClient.Aliases(nil, opensearchapi.AliasesReq{})
			require.NotNil(t, err)
			require.NotNil(t, httpResp)
			require.Nil(t, res)
			osapitest.VerifyResponse(t, httpResp)
		})
	})
}

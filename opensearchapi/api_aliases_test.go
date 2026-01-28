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

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
)

func TestAliases(t *testing.T) {
	t.Run("Aliases", func(t *testing.T) {
		client, err := testutil.NewClient(t)
		require.NoError(t, err)

		index := testutil.MustUniqueString(t, "test-aliases")
		t.Cleanup(func() {
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{Indices: []string{index}})
		})

		_, err = client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		t.Run("with request", func(t *testing.T) {
			resp, err := client.Aliases(
				t.Context(),
				opensearchapi.AliasesReq{
					Body: strings.NewReader(
						`{"actions":[{"add":{"index":"` + index + `","alias":"logs"}},` +
							`{"remove":{"index":"` + index + `","alias":"logs"}}]}`,
					),
				},
			)
			require.NoError(t, err)
			require.NotEmpty(t, resp)
			require.NotEmpty(t, resp.Inspect().Response)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})

		t.Run("inspect", func(t *testing.T) {
			failingClient, err := osapitest.CreateFailingClient()
			require.NoError(t, err)

			res, err := failingClient.Aliases(t.Context(), opensearchapi.AliasesReq{})
			require.Error(t, err)
			require.NotNil(t, res)
			osapitest.VerifyInspect(t, res.Inspect())
		})
	})
}

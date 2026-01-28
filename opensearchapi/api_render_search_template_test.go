// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
)

func TestRenderSearchTemplate(t *testing.T) {
	client, err := ostest.NewClient(t)
	require.NoError(t, err)

	if ostest.IsSecure() {
		major, patch, _, err := ostest.GetVersion(t, client)
		assert.NoError(t, err)
		if major == 2 && (patch == 10 || patch == 11) {
			t.Skipf("Skipping %s due to: https://github.com/opensearch-project/security/issues/3672", t.Name())
		}
	}

	testScript := "test-search-template"
	t.Cleanup(func() {
		client.Script.Delete(t.Context(), opensearchapi.ScriptDeleteReq{ScriptID: testScript})
	})

	_, err = client.Script.Put(
		t.Context(),
		opensearchapi.ScriptPutReq{
			ScriptID: testScript,
			Body: strings.NewReader(`{"script":{"lang":"mustache","source":{"from":"{{from}}{{^from}}0{{/from}}",` +
				`"size":"{{size}}{{^size}}10{{/size}}","query":{"match":{"play_name":""}}},` +
				`"params":{"play_name":"Henry IV"}}}`),
		},
	)
	require.NoError(t, err)

	t.Run("with request", func(t *testing.T) {
		resp, err := client.RenderSearchTemplate(
			t.Context(),
			opensearchapi.RenderSearchTemplateReq{
				TemplateID: testScript,
				Body:       strings.NewReader(fmt.Sprintf(`{"id":"%s","params":{"play_name":"Henry IV"}}`, testScript)),
			},
		)
		require.NoError(t, err)
		assert.NotEmpty(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.NoError(t, err)

		res, err := failingClient.RenderSearchTemplate(t.Context(), opensearchapi.RenderSearchTemplateReq{})
		assert.Error(t, err)
		assert.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

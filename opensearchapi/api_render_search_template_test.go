// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration

package opensearchapi_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v3/internal/test"
	"github.com/opensearch-project/opensearch-go/v3/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v3/opensearchapi/internal/test"
)

func TestRenderSearchTemplate(t *testing.T) {
	client, err := ostest.NewClient()
	require.Nil(t, err)

	if ostest.IsSecure() {
		major, patch, _, err := ostest.GetVersion(client)
		assert.Nil(t, err)
		if major == 2 && (patch == 10 || patch == 11) {
			t.Skipf("Skiping %s due to: https://github.com/opensearch-project/security/issues/3672", t.Name())
		}
	}

	testScript := "test-search-template"
	t.Cleanup(func() {
		client.Script.Delete(nil, opensearchapi.ScriptDeleteReq{ScriptID: testScript})
	})

	_, err = client.Script.Put(
		nil,
		opensearchapi.ScriptPutReq{
			ScriptID: testScript,
			Body:     strings.NewReader(`{"script":{"lang":"mustache","source":{"from":"{{from}}{{^from}}0{{/from}}","size":"{{size}}{{^size}}10{{/size}}","query":{"match":{"play_name":""}}},"params":{"play_name":"Henry IV"}}}`),
		},
	)
	require.Nil(t, err)

	t.Run("with request", func(t *testing.T) {
		resp, err := client.RenderSearchTemplate(
			nil,
			opensearchapi.RenderSearchTemplateReq{
				TemplateID: testScript,
				Body:       strings.NewReader(fmt.Sprintf(`{"id":"%s","params":{"play_name":"Henry IV"}}`, testScript)),
			},
		)
		require.Nil(t, err)
		assert.NotEmpty(t, resp)
		ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
	})

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.Nil(t, err)

		res, err := failingClient.RenderSearchTemplate(nil, opensearchapi.RenderSearchTemplateReq{})
		assert.NotNil(t, err)
		assert.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

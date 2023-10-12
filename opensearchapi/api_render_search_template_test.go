// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
//go:build integration

package opensearchapi_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v2/opensearchapi/internal/test"
)

func TestRenderSearchTemplate(t *testing.T) {
	client, err := opensearchapi.NewDefaultClient()
	require.Nil(t, err)

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
		osapitest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
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

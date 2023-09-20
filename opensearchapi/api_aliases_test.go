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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v2/opensearchapi/internal/test"
)

func TestAliases(t *testing.T) {
	t.Run("Aliases", func(t *testing.T) {
		client, err := opensearchapi.NewDefaultClient()
		require.Nil(t, err)

		index := "test-aliases"
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{index}})
		})

		_, err = client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		t.Run("with request", func(t *testing.T) {
			resp, err := client.Aliases(
				nil,
				opensearchapi.AliasesReq{
					Body: strings.NewReader(`{"actions":[{"add":{"index":"test-aliases","alias":"logs"}},{"remove":{"index":"test-aliases","alias":"logs"}}]}`),
				},
			)
			require.Nil(t, err)
			require.NotEmpty(t, resp)
			require.NotEmpty(t, resp.Inspect().Response)
			osapitest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})

		t.Run("inspect", func(t *testing.T) {
			failingClient, err := osapitest.CreateFailingClient()
			require.Nil(t, err)

			res, err := failingClient.Aliases(nil, opensearchapi.AliasesReq{})
			require.NotNil(t, err)
			require.NotNil(t, res)
			osapitest.VerifyInspect(t, res.Inspect())
		})
	})
}

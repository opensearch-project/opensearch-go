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

func TestBulkClient(t *testing.T) {
	client, err := opensearchapi.NewDefaultClient()
	require.Nil(t, err)

	index := "test-bulk"
	t.Cleanup(func() {
		client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})

	tests := []struct {
		Name    string
		Request opensearchapi.BulkReq
	}{
		{
			Name: "with index",
			Request: opensearchapi.BulkReq{
				Index: index,
				Body:  strings.NewReader("{\"index\": {}}\n{\"test\": 1234}\n{\"create\": {}}\n{\"test\": 5678}\n"),
			},
		},
		{
			Name: "without index",
			Request: opensearchapi.BulkReq{
				Body: strings.NewReader(
					fmt.Sprintf("{\"index\": {\"_index\": \"%s\"}}\n{\"test\": 1234}\n{\"create\": {\"_index\": \"%s\"}}\n{\"test\": 5678}\n", index, index),
				),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			res, err := client.Bulk(
				nil,
				test.Request,
			)
			require.Nil(t, err)
			assert.NotEmpty(t, res)
			osapitest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
		})
	}
	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient()
		require.Nil(t, err)

		res, err := failingClient.Bulk(nil, opensearchapi.BulkReq{Index: index})
		assert.NotNil(t, err)
		assert.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

//go:build integration
// +build integration

package opensearchutil_test

import (
	"strings"
	"testing"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v2/opensearchutil"
)

func TestJSONReaderIntegration(t *testing.T) {
	t.Run("Index and search", func(t *testing.T) {
		var (
			res *opensearchapi.Response
			err error
		)

		client, err := opensearch.NewDefaultClient()
		if err != nil {
			t.Fatalf("Error creating the client: %s\n", err)
		}

		client.Indices.Delete([]string{"test"}, client.Indices.Delete.WithIgnoreUnavailable(true))

		doc := struct {
			Title string `json:"title"`
		}{Title: "Foo Bar"}

		res, err = client.Index("test", opensearchutil.NewJSONReader(&doc), client.Index.WithRefresh("true"))
		if err != nil {
			t.Fatalf("Error getting response: %s", err)
		}
		defer res.Body.Close()

		query := map[string]interface{}{
			"query": map[string]interface{}{
				"match": map[string]interface{}{
					"title": "foo",
				},
			},
		}

		res, err = client.Search(
			client.Search.WithIndex("test"),
			client.Search.WithBody(opensearchutil.NewJSONReader(&query)),
			client.Search.WithPretty(),
		)
		if err != nil {
			t.Fatalf("Error getting response: %s", err)
		}
		defer res.Body.Close()

		if !strings.Contains(res.String(), "Foo Bar") {
			t.Errorf("Unexpected response: %s", res)
		}
	})
}

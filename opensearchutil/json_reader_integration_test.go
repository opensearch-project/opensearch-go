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

//go:build integration && (core || opensearchutil)

package opensearchutil_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
)

func TestJSONReaderIntegration(t *testing.T) {
	t.Run("Index and search", func(t *testing.T) {
		ctx := t.Context()

		client, err := testutil.NewClient(t)
		if err != nil {
			t.Fatalf("Error creating the client: %s\n", err)
		}

		client.Indices.Delete(ctx, opensearchapi.IndicesDeleteReq{
			Indices: []string{"test"},
			Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
		})

		doc := struct {
			Title string `json:"title"`
		}{Title: "Foo Bar"}

		_, err = client.Index(ctx, opensearchapi.IndexReq{
			Index:  "test",
			Body:   opensearchutil.NewJSONReader(&doc),
			Params: opensearchapi.IndexParams{Refresh: "true"},
		})
		if err != nil {
			t.Fatalf("Error getting response: %s", err)
		}

		query := map[string]any{
			"query": map[string]any{
				"match": map[string]any{
					"title": "foo",
				},
			},
		}
		req := &opensearchapi.SearchReq{
			Indices: []string{"test"},
			Body:    opensearchutil.NewJSONReader(&query),
		}
		res, err := client.Search(ctx, req)
		if err != nil {
			t.Fatalf("Error getting response: %s", err)
		}
		containsFooBar := func(c opensearchapi.SearchHit) bool {
			return strings.Contains(fmt.Sprintf("%v", c.Source), "Foo Bar")
		}
		if len(res.Hits.Hits) == 0 && !slices.ContainsFunc(res.Hits.Hits, containsFooBar) {
			t.Errorf("Unexpected response: %v", res)
		}
	})
}

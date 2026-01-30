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

//go:build integration && (core || opensearchtransport) && multinode

package opensearchtransport_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
)

var (
	_ = fmt.Print
)

func TestTransportSelector(t *testing.T) {
	NodeName := func(t *testing.T, transport opensearchtransport.Interface) string {
		req, err := http.NewRequest("GET", "/", nil)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		res, err := transport.Perform(req)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}

		fmt.Printf("> GET %s\n", req.URL)

		r := struct {
			Name string
		}{}

		if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
			t.Fatalf("Error parsing the response body: %s", err)
		}

		return r.Name
	}

	t.Run("RoundRobin", func(t *testing.T) {
		var (
			node string
		)
		transport, _ := opensearchtransport.New(opensearchtransport.Config{URLs: []*url.URL{
			{Scheme: "http", Host: "localhost:9200"},
			{Scheme: "http", Host: "localhost:9201"},
		}})

		node = NodeName(t, transport)
		if node != "opensearch-node1" {
			t.Errorf("Unexpected node, want=opensearch-node1, got=%s", node)
		}

		node = NodeName(t, transport)
		if node != "opensearch-node2" {
			t.Errorf("Unexpected node, want=opensearch-node2, got=%s", node)
		}

		node = NodeName(t, transport)
		if node != "opensearch-node1" {
			t.Errorf("Unexpected node, want=opensearch-node1, got=%s", node)
		}
	})
}

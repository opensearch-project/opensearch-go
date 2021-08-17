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

package opensearchapi_test

import (
	"log"

	"github.com/opensearch-project/opensearch-go"
)

func ExampleResponse_IsError() {
	client, _ := opensearch.NewDefaultClient()

	res, err := client.Info()

	// Handle connection errors
	//
	if err != nil {
		log.Fatalf("ERROR: %v", err)
	}
	defer res.Body.Close()

	// Handle error response (4xx, 5xx)
	//
	if res.IsError() {
		log.Fatalf("ERROR: %s", res.Status())
	}

	// Handle successful response (2xx)
	//
	log.Println(res)
}

func ExampleResponse_Status() {
	client, _ := opensearch.NewDefaultClient()

	res, _ := client.Info()
	log.Println(res.Status())

	// 200 OK
}

func ExampleResponse_String() {
	client, _ := opensearch.NewDefaultClient()

	res, _ := client.Info()
	log.Println(res.String())

	// [200 OK] {
	// "name" : "opensearch1",
	// "cluster_name" : "opensearch-go",
	// ...
	// }
}

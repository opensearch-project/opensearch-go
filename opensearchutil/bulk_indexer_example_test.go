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

//go:build !integration

package opensearchutil_test

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchutil"
)

func ExampleNewBulkIndexer() {
	log.SetFlags(0)

	// Create the OpenSearch client
	//
	client, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			// Retry on 429 TooManyRequests statuses
			//
			RetryOnStatus: []int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusTooManyRequests},

			// A simple incremental backoff function
			//
			RetryBackoff: func(i int) time.Duration { return time.Duration(i) * 100 * time.Millisecond },

			// Retry up to 5 attempts
			//
			MaxRetries: 5,
		},
	})
	if err != nil {
		log.Fatalf("Error creating the client: %s", err)
	}
	defer func() { _ = client.Close() }()

	// Create the bulk client
	//
	indexer, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
		Client:     client,     // The OpenSearch client
		Index:      "my-index", // The default index name
		NumWorkers: 4,          // The number of worker goroutines (default: number of CPUs)
		FlushBytes: 5e+6,       // The flush threshold in bytes (default: 5M)
	})
	if err != nil {
		log.Panicf("Error creating the indexer: %s", err)
	}

	// Add an item to the bulk client
	//
	// source is the kind of value Metadata carries: where the document came
	// from, so a callback can report on it without a closure per item.
	type source struct {
		Topic  string
		Offset int64
	}

	err = indexer.Add(
		context.Background(),
		opensearchutil.BulkIndexerItem{
			// Action field configures the operation to perform (index, create, delete, update)
			Action: "index",

			// DocumentID is the optional document ID
			DocumentID: "doc-1",

			// Body is an `io.Reader` with the payload
			Body: strings.NewReader(`{"title":"Test"}`),

			// Metadata is opaque to the bulk client: it is never sent to the
			// cluster and comes back unchanged in the callbacks below
			Metadata: source{Topic: "events", Offset: 42},

			// OnSuccess is the optional callback for each successful operation
			OnSuccess: func(
				ctx context.Context,
				item opensearchutil.BulkIndexerItem,
				res opensearchapi.BulkRespItem,
			) {
				from, _ := item.Metadata.(source)
				fmt.Printf("[%d] %v test/%s from %s@%d", res.Status, res.Result, item.DocumentID, from.Topic, from.Offset)
			},

			// OnError is the optional callback for each failed operation
			OnError: func(
				ctx context.Context,
				item opensearchutil.BulkIndexerItem,
				err *opensearchutil.BulkError,
			) {
				// err says which layer failed and why; for an item the cluster
				// rejected, its message carries the cluster's reason.
				from, _ := item.Metadata.(source)
				log.Printf("ERROR: %s@%d: %s", from.Topic, from.Offset, err)
			},
		},
	)
	if err != nil {
		log.Panicf("Unexpected error: %s", err)
	}

	// Close the bulk client channel and flush remaining items
	//
	if err := indexer.Close(context.Background()); err != nil {
		log.Panicf("Unexpected error: %s", err)
	}

	// Report the bulk client statistics
	//
	stats := indexer.Stats()
	if stats.NumFailed > 0 {
		log.Panicf("Indexed [%d] documents with [%d] errors", stats.NumFlushed, stats.NumFailed)
	}
	log.Printf("Successfully indexed [%d] documents", stats.NumFlushed)

	// For optimal performance, consider using a third-party package for JSON decoding and HTTP transport.
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package compress_test

import (
	"context"

	compress "github.com/opensearch-project/opensearch-go/compress/v5"
	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchutil"
)

// Example compresses every request body the client sends. The server must
// accept the Content-Encoding the compressor sets, so confirm it does before
// enabling a codec on a production client.
func Example() {
	c, err := compress.Gzip(compress.DefaultLevel)
	if err != nil {
		panic(err)
	}

	client, err := opensearch.NewClient(opensearch.Config{
		Addresses:  []string{"http://localhost:9200"},
		Compressor: c,
	})
	if err != nil {
		panic(err)
	}
	defer func() { _ = client.Close() }()
}

// Example_bulkIndexer compresses only the bulk client's requests, since bulk
// bodies are the largest the client sends. The application's other traffic goes
// through client, which has no Compressor and sends bodies uncompressed.
func Example_bulkIndexer() {
	client, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: []string{"http://localhost:9200"},
		},
	})
	if err != nil {
		panic(err)
	}
	defer func() { _ = client.Close() }()

	c, err := compress.Gzip(compress.DefaultLevel)
	if err != nil {
		panic(err)
	}

	bulkClient, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Addresses:  []string{"http://localhost:9200"},
			Compressor: c,
		},
	})
	if err != nil {
		panic(err)
	}
	defer func() { _ = bulkClient.Close() }()

	indexer, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
		Client: bulkClient,
		Index:  "my-index",
	})
	if err != nil {
		panic(err)
	}
	defer func() { _ = indexer.Close(context.Background()) }()
}

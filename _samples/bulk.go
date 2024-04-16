// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

func main() {
	if err := example(); err != nil {
		fmt.Println(fmt.Sprintf("Error: %s", err))
		os.Exit(1)
	}
}

func example() error {
	// Initialize the client with SSL/TLS enabled.
	client, err := opensearchapi.NewClient(
		opensearchapi.Config{
			Client: opensearch.Config{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // For testing only. Use certificate for validation.
				},
				Addresses: []string{"https://localhost:9200"},
				Username:  "admin", // For testing only. Don't store credentials in code.
				Password:  "myStrongPassword123!",
			},
		},
	)
	if err != nil {
		return err
	}

	ctx := context.Background()

	movies := "movies"
	books := "books"

	createResp, err := client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: movies})
	if err != nil {
		return err
	}
	fmt.Printf("Index created: %t\n", createResp.Acknowledged)

	createResp, err = client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: books})
	if err != nil {
		return err
	}
	fmt.Printf("Index created: %t\n", createResp.Acknowledged)

	bulkResp, err := client.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Body: strings.NewReader(`{ "index": { "_index": "movies", "_id": 1 } }
{ "title": "Beauty and the Beast", "year": 1991 }
{ "index": { "_index": "movies", "_id": 2 } }
{ "title": "Beauty and the Beast - Live Action", "year": 2017 }
{ "index": { "_index": "books", "_id": 1 } }
{ "title": "The Lion King", "year": 1994 }
`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err := json.MarshalIndent(bulkResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Bulk Resp:\n%s\n", string(respAsJson))

	bulkResp, err = client.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Body: strings.NewReader(`{ "create": { "_index": "movies" } }
{ "title": "Beauty and the Beast 2", "year": 2030 }
{ "create": { "_index": "movies", "_id": 1 } }
{ "title": "Beauty and the Beast 3", "year": 2031 }
{ "create": { "_index": "movies", "_id": 2 } }
{ "title": "Beauty and the Beast 4", "year": 2049 }
{ "create": { "_index": "books" } }
{ "title": "The Lion King 2", "year": 1998 }
`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(bulkResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Bulk Resp:\n%s\n", string(respAsJson))

	bulkResp, err = client.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Body: strings.NewReader(`{ "update": { "_index": "movies", "_id": 1 } }
{ "doc": { "year": 1992 } }
{ "update": { "_index": "movies", "_id": 1 } }
{ "doc": { "year": 2018 } }
`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(bulkResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Bulk Resp:\n%s\n", string(respAsJson))

	bulkResp, err = client.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Body: strings.NewReader(`{ "delete": { "_index": "movies", "_id": 1 } }
{ "delete": { "_index": "movies", "_id": 2 } }
`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(bulkResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Bulk Resp:\n%s\n", string(respAsJson))

	bulkResp, err = client.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Body: strings.NewReader(`{ "create": { "_index": "movies", "_id": 3 } }
{ "title": "Beauty and the Beast 5", "year": 2050 }
{ "create": { "_index": "movies", "_id": 4 } }
{ "title": "Beauty and the Beast 6", "year": 2051 }
{ "update": { "_index": "movies", "_id": 3 } }
{ "doc": { "year": 2052 } }
{ "delete": { "_index": "movies", "_id": 4 } }
`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(bulkResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Bulk Resp:\n%s\n", string(respAsJson))

	bulkResp, err = client.Bulk(
		ctx,
		opensearchapi.BulkReq{
			Body: strings.NewReader("{\"delete\":{\"_index\":\"movies\",\"_id\":1}}\n"),
		},
	)
	if err != nil {
		return err
	}
	for _, item := range bulkResp.Items {
		for operation, resp := range item {
			if resp.Status > 299 {
				fmt.Printf("Bulk %s Error: %s\n", operation, resp.Result)
			}
		}
	}

	delResp, err := client.Indices.Delete(
		ctx,
		opensearchapi.IndicesDeleteReq{
			Indices: []string{"movies", "books"},
			Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Deleted: %t\n", delResp.Acknowledged)

	return nil
}

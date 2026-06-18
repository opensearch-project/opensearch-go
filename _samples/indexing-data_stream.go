// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
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
				InsecureSkipVerify: true, // For testing only. Use certificate for validation.
				Addresses:          []string{"https://localhost:9200"},
				Username:           "admin", // For testing only. Don't store credentials in code.
				Password:           "myStrongPassword123!",
			},
		},
	)
	if err != nil {
		return err
	}

	ctx := context.Background()

	tempCreateResp, err := client.Indices.PutIndexTemplate(
		ctx,
		opensearchapi.IndicesPutIndexTemplateReq{
			Name: "books",
			BodyReader: strings.NewReader(`{
    		"index_patterns": ["books-nonfiction"],
    		"template": {
    		  "settings": {
    		    "index": {
    		      "number_of_shards": 3,
    		      "number_of_replicas": 0
    		    }
    		  }
    		},
				"data_stream": {},
				"priority": 50
		}`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Index Tempalte created: %t\n", tempCreateResp.Acknowledged)

	createResp, err := client.Indices.CreateDataStream(ctx, opensearchapi.IndicesCreateDataStreamReq{Name: "books-nonfiction"})
	if err != nil {
		return err
	}
	fmt.Printf("Created: %t\n", createResp.Acknowledged)

	getResp, err := client.Indices.GetDataStream(ctx, nil)
	if err != nil {
		return err
	}
	respAsJson, err := json.MarshalIndent(getResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get DataStream:\n%s\n", string(respAsJson))

	getResp, err = client.Indices.GetDataStream(ctx, &opensearchapi.IndicesGetDataStreamReq{Name: []string{"books-nonfiction"}})
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(getResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get DataStream:\n%s\n", string(respAsJson))

	statsResp, err := client.Indices.DataStreamsStats(ctx, nil)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(statsResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Stats DataStream:\n%s\n", string(respAsJson))

	delResp, err := client.Indices.DeleteDataStream(ctx, &opensearchapi.IndicesDeleteDataStreamReq{Name: []string{"books-nonfiction"}})
	if err != nil {
		return err
	}
	fmt.Printf("DataStream deleted: %t\n", delResp.Acknowledged)

	delTempResp, err := client.Indices.DeleteIndexTemplate(ctx, opensearchapi.IndicesDeleteIndexTemplateReq{Name: "books"})
	if err != nil {
		return err
	}
	fmt.Printf("Deleted templates: %t\n", delTempResp.Acknowledged)

	return nil
}

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

	tempCreateResp, err := client.IndexTemplate.Create(
		ctx,
		opensearchapi.IndexTemplateCreateReq{
			IndexTemplate: "books",
			Body: strings.NewReader(`{
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

	createResp, err := client.DataStream.Create(ctx, opensearchapi.DataStreamCreateReq{DataStream: "books-nonfiction"})
	if err != nil {
		return err
	}
	fmt.Printf("Created: %t\n", createResp.Acknowledged)

	getResp, err := client.DataStream.Get(ctx, nil)
	if err != nil {
		return err
	}
	respAsJson, err := json.MarshalIndent(getResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get DataStream:\n%s\n", string(respAsJson))

	getResp, err = client.DataStream.Get(ctx, &opensearchapi.DataStreamGetReq{DataStreams: []string{"books-nonfiction"}})
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(getResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get DataStream:\n%s\n", string(respAsJson))

	statsResp, err := client.DataStream.Stats(ctx, nil)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(statsResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Stats DataStream:\n%s\n", string(respAsJson))

	delResp, err := client.DataStream.Delete(ctx, opensearchapi.DataStreamDeleteReq{DataStream: "books-nonfiction"})
	if err != nil {
		return err
	}
	fmt.Printf("DataStream deleted: %t\n", delResp.Acknowledged)

	delTempResp, err := client.IndexTemplate.Delete(ctx, opensearchapi.IndexTemplateDeleteReq{IndexTemplate: "books"})
	if err != nil {
		return err
	}
	fmt.Printf("Deleted templates: %t\n", delTempResp.Acknowledged)

	return nil
}

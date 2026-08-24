// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// This sample demonstrates raw JSON REST requests when no typed method fits.
//
// Learn more:
//   - Guide: https://github.com/opensearch-project/opensearch-go/blob/main/guides/usage-json.md
//   - API reference: https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchapi

package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

const IndexName = "go-test-index1"

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

	///

	infoRequest, err := http.NewRequest(http.MethodGet, "/", nil)
	if err != nil {
		return err
	}

	infoResponse, err := client.Client.Stream(infoRequest)
	if err != nil {
		return err
	}
	resBody, err := io.ReadAll(infoResponse.Body)
	infoResponse.Body.Close()
	if err != nil {
		return err
	}
	fmt.Printf("client info: %s\n", resBody)

	///

	var index_body = strings.NewReader(`{
    "settings": {
        "index": {
            "number_of_shards": 2,
            "number_of_replicas": 1
        }
    },
    "mappings": {
        "properties": {
            "title": {
                "type": "text"
            },
            "year": {
                "type": "integer"
            }
        }
    }
}`)

	createIndexRequest, err := http.NewRequest(http.MethodPut, "/movies", index_body)
	if err != nil {
		return err
	}
	createIndexRequest.Header["Content-Type"] = []string{"application/json"}
	createIndexResp, err := client.Client.Stream(createIndexRequest)
	if err != nil {
		return err
	}
	createIndexRespBody, err := io.ReadAll(createIndexResp.Body)
	createIndexResp.Body.Close()
	if err != nil {
		return err
	}
	fmt.Println("create index: ", string(createIndexRespBody))

	///

	query := strings.NewReader(`{
    "size": 5,
    "query": {
        "multi_match": {
        "query": "miller",
        "fields": ["title^2", "director"]
        }
    }
}`)
	searchRequest, err := http.NewRequest(http.MethodPost, "/movies/_search", query)
	if err != nil {
		return err
	}
	searchRequest.Header["Content-Type"] = []string{"application/json"}
	searchResp, err := client.Client.Stream(searchRequest)
	if err != nil {
		return err
	}
	searchRespBody, err := io.ReadAll(searchResp.Body)
	searchResp.Body.Close()
	if err != nil {
		return err
	}
	fmt.Println("search: ", string(searchRespBody))

	///

	deleteIndexRequest, err := http.NewRequest(http.MethodDelete, "/movies", nil)
	if err != nil {
		return err
	}
	deleteIndexResp, err := client.Client.Stream(deleteIndexRequest)
	if err != nil {
		return err
	}
	deleteIndexRespBody, err := io.ReadAll(deleteIndexResp.Body)
	deleteIndexResp.Body.Close()
	if err != nil {
		return err
	}
	fmt.Println("delete index: ", string(deleteIndexRespBody))

	return nil
}

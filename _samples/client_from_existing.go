// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"

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
	// Create a base opensearch.Client with custom configuration
	osClient, err := opensearch.NewClient(opensearch.Config{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // For testing only. Use certificate for validation.
		},
		Addresses: []string{"https://localhost:9200"},
		Username:  "admin", // For testing only. Don't store credentials in code.
		Password:  "myStrongPassword123!",
	})
	if err != nil {
		return err
	}

	// Create an opensearchapi.Client directly from the existing opensearch.Client
	// This is useful when you want to:
	// - Share the same underlying connection pool between clients
	// - Wrap an existing client without recreating the transport
	// - Maintain a single configured client instance
	apiClient := opensearchapi.NewFromClient(osClient)

	fmt.Println("Successfully created opensearchapi.Client from opensearch.Client")
	fmt.Printf("Both clients share the same transport and configuration\n")

	// You can use the apiClient for high-level operations
	_ = apiClient

	return nil
}

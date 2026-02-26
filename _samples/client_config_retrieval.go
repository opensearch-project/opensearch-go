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
	// Create a base opensearch.Client
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

	// Retrieve the configuration that was used to create the client
	// This is useful for:
	// - Inspecting the client's configuration
	// - Creating a new client with the same or modified configuration
	// - Logging/debugging configuration details
	config := osClient.GetConfig()

	fmt.Printf("Original client configuration:\n")
	fmt.Printf("  Addresses: %v\n", config.Addresses)
	fmt.Printf("  Username: %s\n", config.Username)
	fmt.Printf("  DisableRetry: %v\n", config.DisableRetry)
	fmt.Printf("  MaxRetries: %d\n", config.MaxRetries)

	// Create a new opensearchapi.Client using the retrieved configuration
	// This creates a completely new client with independent transport/connection pool
	apiClient, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: *config,
	})
	if err != nil {
		return err
	}

	fmt.Println("\nSuccessfully created opensearchapi.Client using retrieved configuration")

	// You can use the apiClient for high-level operations
	_ = apiClient

	return nil
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchapi_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil/mockhttp"
)

var defaultRoundTripFunc = func(req *http.Request) (*http.Response, error) {
	response := &http.Response{Header: http.Header{}}

	if req.URL.Path == "/" {
		response.Body = io.NopCloser(strings.NewReader(`{
		  "version" : {
			"number" : "1.0.0",
			"distribution" : "opensearch"
		  }
		}`))
		response.Header.Add("Content-Type", "application/json")
	}

	return response, nil
}

func TestNewFromClient(t *testing.T) {
	t.Run("creates api client from opensearch client", func(t *testing.T) {
		// Create a base opensearch.Client
		osClient, err := opensearch.NewClient(opensearch.Config{
			Addresses: []string{"http://localhost:9200"},
			Username:  "admin",
			Password:  "password",
			Transport: mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc),
		})
		require.NoError(t, err)
		require.NotNil(t, osClient)

		// Create an opensearchapi.Client from the opensearch.Client
		apiClient := opensearchapi.NewFromClient(osClient)

		// Verify the api client was created successfully
		require.NotNil(t, apiClient)
		require.NotNil(t, apiClient.Client)

		// Verify the underlying client is the same
		require.Equal(t, osClient, apiClient.Client)

		// Verify all sub-clients are initialized
		require.NotNil(t, apiClient.Cat)
		require.NotNil(t, apiClient.Cluster)
		require.NotNil(t, apiClient.Dangling)
		require.NotNil(t, apiClient.Document)
		require.NotNil(t, apiClient.Indices)
		require.NotNil(t, apiClient.Nodes)
		require.NotNil(t, apiClient.Script)
		require.NotNil(t, apiClient.ComponentTemplate)
		require.NotNil(t, apiClient.IndexTemplate)
		require.NotNil(t, apiClient.Template)
		require.NotNil(t, apiClient.DataStream)
		require.NotNil(t, apiClient.PointInTime)
		require.NotNil(t, apiClient.Ingest)
		require.NotNil(t, apiClient.Tasks)
		require.NotNil(t, apiClient.Scroll)
		require.NotNil(t, apiClient.Snapshot)
	})

	t.Run("shares transport with original client", func(t *testing.T) {
		// Create a base opensearch.Client
		osClient, err := opensearch.NewClient(opensearch.Config{
			Addresses: []string{"http://localhost:9200"},
			Transport: mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc),
		})
		require.NoError(t, err)

		// Create an opensearchapi.Client from the opensearch.Client
		apiClient := opensearchapi.NewFromClient(osClient)

		// Verify both clients share the same transport
		require.Equal(t, osClient.Transport, apiClient.Client.Transport)
	})

	t.Run("can access config from wrapped client", func(t *testing.T) {
		// Create a base opensearch.Client with specific config
		expectedAddresses := []string{"http://localhost:9200", "http://localhost:9201"}
		expectedUsername := "testuser"
		expectedPassword := "testpass"

		osClient, err := opensearch.NewClient(opensearch.Config{
			Addresses: expectedAddresses,
			Username:  expectedUsername,
			Password:  expectedPassword,
			Transport: mockhttp.NewRoundTripFunc(t, defaultRoundTripFunc),
		})
		require.NoError(t, err)

		// Create an opensearchapi.Client from the opensearch.Client
		apiClient := opensearchapi.NewFromClient(osClient)

		// Retrieve the config through the api client's wrapped opensearch client
		config := apiClient.Client.GetConfig()

		// Verify the config matches what was originally provided
		require.Equal(t, expectedAddresses, config.Addresses)
		require.Equal(t, expectedUsername, config.Username)
		require.Equal(t, expectedPassword, config.Password)
	})
}

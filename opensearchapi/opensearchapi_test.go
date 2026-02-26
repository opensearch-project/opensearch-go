// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchapi_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

type mockTransport struct{}

func (t *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
	}, nil
}

func TestNewFromClient(t *testing.T) {
	t.Run("creates api client from opensearch client", func(t *testing.T) {
		// Create a base opensearch.Client
		osClient, err := opensearch.NewClient(opensearch.Config{
			Addresses: []string{"http://localhost:9200"},
			Username:  "admin",
			Password:  "password",
			Transport: &mockTransport{},
		})
		require.NoError(t, err)
		require.NotNil(t, osClient)

		// Create an opensearchapi.Client from the opensearch.Client
		apiClient := opensearchapi.NewFromClient(osClient)

		// Verify the api client was created successfully
		require.NotNil(t, apiClient)
		assert.NotNil(t, apiClient.Client)

		// Verify the underlying client is the same
		assert.Equal(t, osClient, apiClient.Client)

		// Verify all sub-clients are initialized
		assert.NotNil(t, apiClient.Cat)
		assert.NotNil(t, apiClient.Cluster)
		assert.NotNil(t, apiClient.Dangling)
		assert.NotNil(t, apiClient.Document)
		assert.NotNil(t, apiClient.Indices)
		assert.NotNil(t, apiClient.Nodes)
		assert.NotNil(t, apiClient.Script)
		assert.NotNil(t, apiClient.ComponentTemplate)
		assert.NotNil(t, apiClient.IndexTemplate)
		assert.NotNil(t, apiClient.Template)
		assert.NotNil(t, apiClient.DataStream)
		assert.NotNil(t, apiClient.PointInTime)
		assert.NotNil(t, apiClient.Ingest)
		assert.NotNil(t, apiClient.Tasks)
		assert.NotNil(t, apiClient.Scroll)
		assert.NotNil(t, apiClient.Snapshot)
	})

	t.Run("shares transport with original client", func(t *testing.T) {
		// Create a base opensearch.Client
		osClient, err := opensearch.NewClient(opensearch.Config{
			Addresses: []string{"http://localhost:9200"},
			Transport: &mockTransport{},
		})
		require.NoError(t, err)

		// Create an opensearchapi.Client from the opensearch.Client
		apiClient := opensearchapi.NewFromClient(osClient)

		// Verify both clients share the same transport
		assert.Equal(t, osClient.Transport, apiClient.Client.Transport)
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
			Transport: &mockTransport{},
		})
		require.NoError(t, err)

		// Create an opensearchapi.Client from the opensearch.Client
		apiClient := opensearchapi.NewFromClient(osClient)

		// Retrieve the config through the api client's wrapped opensearch client
		config := apiClient.Client.GetConfig()

		// Verify the config matches what was originally provided
		assert.Equal(t, expectedAddresses, config.Addresses)
		assert.Equal(t, expectedUsername, config.Username)
		assert.Equal(t, expectedPassword, config.Password)
	})
}

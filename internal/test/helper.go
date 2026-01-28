// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//go:build integration

package ostest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wI2L/jsondiff"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

// NewClient returns an opensearchapi.Client that is adjusted for the wanted test case
// and ensures the OpenSearch cluster is ready for requests.
func NewClient(t *testing.T) (*opensearchapi.Client, error) {
	t.Helper()
	config, err := ClientConfig()
	if err != nil {
		return nil, err
	}

	client, err := opensearchapi.NewClient(*config)
	if err != nil {
		return nil, err
	}

	// Always wait for cluster readiness
	err = waitForClusterReady(t, client)
	if err != nil {
		return nil, err
	}

	return client, nil
}

// waitForClusterReady waits for the OpenSearch cluster to be fully ready for API calls.
func waitForClusterReady(t *testing.T, client *opensearchapi.Client) error {
	t.Helper()
	const (
		maxAttempts          = 25
		delayBetweenAttempts = 5 * time.Second
		requestTimeout       = 2 * time.Second
	)

	// Get version for informational logging
	major, minor, patch, err := GetVersion(client, t)
	if err != nil {
		return fmt.Errorf("failed to get OpenSearch version: %w", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	for attempt := range maxAttempts {
		// Basic cluster health check
		resp, err := client.Cluster.Health(ctx, nil)
		if err != nil || resp == nil {
			t.Logf("Waiting %s for cluster readiness (attempt %d/%d)...", delayBetweenAttempts, attempt+1, maxAttempts)
			time.Sleep(delayBetweenAttempts)

			// Reset context for next attempt
			ctx, cancel = context.WithTimeout(t.Context(), requestTimeout)
			defer cancel()
			continue
		}

		// Extended readiness validation
		if err := extendedReadinessCheck(ctx, client); err == nil {
			if attempt > 0 {
				t.Logf("Cluster ready after %d attempts (version %d.%d.%d)", attempt+1, major, minor, patch)
			}
			return nil
		}

		t.Logf("Cluster health OK but readiness validation failed (attempt %d/%d)", attempt+1, maxAttempts)
		time.Sleep(delayBetweenAttempts)

		// Reset context for next attempt
		ctx, cancel = context.WithTimeout(t.Context(), requestTimeout)
		defer cancel()
	}

	return fmt.Errorf("cluster not ready after %d attempts (version %d.%d.%d)", maxAttempts, major, minor, patch)
}

// extendedReadinessCheck performs validation checks to ensure the
// cluster is ready
func extendedReadinessCheck(ctx context.Context, client *opensearchapi.Client) error {
	// Try a simple cluster state request - this exercises more Java serialization paths
	_, err := client.Cluster.State(ctx, nil)
	if err != nil {
		return fmt.Errorf("cluster state check failed: %w", err)
	}

	// Try a simple nodes info request - exercises node-level serialization
	_, err = client.Nodes.Info(ctx, nil)
	if err != nil {
		return fmt.Errorf("nodes info check failed: %w", err)
	}

	return nil
}

// GetVersion gets cluster info and returns version as int's
func GetVersion(client *opensearchapi.Client, t *testing.T) (int64, int64, int64, error) {
	if client == nil {
		return 0, 0, 0, fmt.Errorf("client cannot be nil")
	}
	resp, err := client.Info(t.Context(), nil)
	if err != nil {
		return 0, 0, 0, err
	}
	return opensearch.ParseVersion(resp.Version.Number)
}

// SkipIfBelowVersion skips a test if the cluster version is below a given version
func SkipIfBelowVersion(t *testing.T, client *opensearchapi.Client, majorVersion, patchVersion int64, testName string) {
	t.Helper()
	major, patch, _, err := GetVersion(client, t)
	assert.Nil(t, err)
	if major < majorVersion || (major == majorVersion && patch < patchVersion) {
		t.Skipf("Skipping %s as version %d.%d.x does not support this endpoint", testName, major, patch)
	}
}

// SkipIfNotSecure skips a test that runs against an insecure cluster
func SkipIfNotSecure(t *testing.T) {
	t.Helper()
	if !IsSecure() {
		t.Skipf("Skipping %s as it needs a secured cluster", t.Name())
	}
}

// CompareRawJSONwithParsedJSON is a helper function to determine the difference between the parsed JSON and the raw JSON
// this is helpful to detect missing fields in the go structs
func CompareRawJSONwithParsedJSON(t *testing.T, resp any, rawResp *opensearch.Response) {
	t.Helper()
	if _, ok := os.LookupEnv("OPENSEARCH_GO_SKIP_JSON_COMPARE"); ok {
		return
	}
	require.NotNil(t, rawResp)

	parsedBody, err := json.Marshal(resp)
	require.Nil(t, err)

	body, err := io.ReadAll(rawResp.Body)
	require.Nil(t, err)

	// If the parsedBody and body does not match, then we need to check if we are adding or removing fields
	if string(parsedBody) != string(body) {
		patch, err := jsondiff.CompareJSON(body, parsedBody)
		assert.Nil(t, err)
		operations := make([]jsondiff.Operation, 0)
		for _, operation := range patch {
			// different opensearch version added more field, only check if we miss some fields
			if operation.Type != "add" || (operation.Type == "add" && operation.Path == "") {
				operations = append(operations, operation)
			}
		}
		assert.Empty(t, operations)
		if len(operations) == 0 {
			return
		}
		for _, op := range operations {
			fmt.Printf("%s\n", op)
		}
		fmt.Printf("%s\n", body)
	}
}

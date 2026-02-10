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
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wI2L/jsondiff"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
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
	err = WaitForClusterReady(t, client)
	if err != nil {
		return nil, err
	}

	return client, nil
}

// logInfoFailure logs information about Info endpoint failures during cluster readiness checks.
func logInfoFailure(t *testing.T, attempt, maxAttempts int, err error, urls []*url.URL, delayBetweenAttempts time.Duration) {
	t.Helper()
	// Only log after first few attempts to reduce noise (HTTPS cold start can be slow)
	if attempt >= 2 {
		t.Logf("Waiting %s for cluster readiness (attempt %d/%d) - Info error: %v - URLs=%v, SECURE_INTEGRATION=%q, OPENSEARCH_VERSION=%q",
			delayBetweenAttempts, attempt+1, maxAttempts, err, urls, os.Getenv("SECURE_INTEGRATION"), os.Getenv("OPENSEARCH_VERSION"))
	}
}

// WaitForClusterReady waits for the OpenSearch cluster to be fully ready for API calls.
// This function is exported so tests that create clients manually can also wait for readiness.
func WaitForClusterReady(t *testing.T, client *opensearchapi.Client) error {
	t.Helper()
	if client == nil || client.Client == nil {
		return fmt.Errorf("client and client.Client must not be nil")
	}

	const (
		maxAttempts          = 25
		delayBetweenAttempts = 5 * time.Second
		requestTimeout       = 15 * time.Second // Increased to allow for HTTPS handshake + multiple requests
	)

	var major, minor, patch int64
	versionKnown := false

	// Log the URLs we're connecting to for debugging
	urls := client.Client.Transport.(*opensearchtransport.Client).URLs()

	for attempt := range maxAttempts {
		// Create a new context for this attempt
		ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)

		// Basic health check using Info endpoint (more reliable during startup)
		infoResp, err := client.Info(ctx, nil)
		if err != nil || infoResp == nil {
			cancel() // Clean up context before sleeping
			if err != nil {
				logInfoFailure(t, attempt, maxAttempts, err, urls, delayBetweenAttempts)
				time.Sleep(delayBetweenAttempts)
				continue
			}

			// err is nil but response is nil - only log after first few attempts
			if attempt >= 2 {
				t.Logf("Waiting %s for cluster readiness (attempt %d/%d) - nil response", delayBetweenAttempts, attempt+1, maxAttempts)
			}
			time.Sleep(delayBetweenAttempts)
			continue
		}

		// Capture version on first successful response
		if !versionKnown {
			major, minor, patch, _ = opensearch.ParseVersion(infoResp.Version.Number)
			versionKnown = true
		}

		// Extended readiness validation - pass parent context, not the one we just used
		if err := extendedReadinessCheck(t.Context(), client); err == nil {
			cancel() // Clean up Info request context before returning
			if attempt > 0 {
				t.Logf("Cluster ready after %d attempts (version %d.%d.%d)", attempt+1, major, minor, patch)
			}
			return nil
		} else {
			cancel() // Clean up Info request context before sleeping
			// Only log extended check failures after first few attempts
			if attempt >= 2 {
				t.Logf("Cluster health OK but readiness validation failed (attempt %d/%d) - error: %v", attempt+1, maxAttempts, err)
			}
			time.Sleep(delayBetweenAttempts)
		}
	}

	if versionKnown {
		return fmt.Errorf("cluster not ready after %d attempts (version %d.%d.%d) - this could indicate: "+
			"(1) wrong credentials, (2) cluster still starting, or (3) network/SSL issues - "+
			"config: URLs=%v, SECURE_INTEGRATION=%q, OPENSEARCH_VERSION=%q",
			maxAttempts, major, minor, patch, urls, os.Getenv("SECURE_INTEGRATION"), os.Getenv("OPENSEARCH_VERSION"))
	}
	return fmt.Errorf("cluster not ready after %d attempts - this could indicate: "+
		"(1) wrong credentials, (2) cluster still starting, or (3) network/SSL issues - "+
		"config: URLs=%v, SECURE_INTEGRATION=%q, OPENSEARCH_VERSION=%q",
		maxAttempts, urls, os.Getenv("SECURE_INTEGRATION"), os.Getenv("OPENSEARCH_VERSION"))
}

// extendedReadinessCheck performs validation checks to ensure the cluster is ready.
// Each check creates its own context with a fresh timeout to avoid cascading timeouts.
func extendedReadinessCheck(parentCtx context.Context, client *opensearchapi.Client) error {
	const checkTimeout = 10 * time.Second

	// Try a simple cluster state request - this exercises more Java serialization paths
	stateCtx, stateCancel := context.WithTimeout(parentCtx, checkTimeout)
	defer stateCancel()
	_, err := client.Cluster.State(stateCtx, nil)
	if err != nil {
		return fmt.Errorf("cluster state check failed: %w", err)
	}

	// Try a simple nodes info request - exercises node-level serialization
	nodesCtx, nodesCancel := context.WithTimeout(parentCtx, checkTimeout)
	defer nodesCancel()
	_, err = client.Nodes.Info(nodesCtx, nil)
	if err != nil {
		return fmt.Errorf("nodes info check failed: %w", err)
	}

	return nil
}

// GetVersion gets cluster info and returns version as int's
func GetVersion(t *testing.T, client *opensearchapi.Client) (int64, int64, int64, error) {
	t.Helper()
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
	major, patch, _, err := GetVersion(t, client)
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

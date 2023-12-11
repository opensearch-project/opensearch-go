// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osapitest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wI2L/jsondiff"

	"github.com/opensearch-project/opensearch-go/v3"
	"github.com/opensearch-project/opensearch-go/v3/opensearchapi"
)

// Response is a dummy interface to run tests with Inspect()
type Response interface {
	Inspect() opensearchapi.Inspect
}

// CreateFailingClient returns an opensearchapi.Client that always return 400 with an empty object as body
func CreateFailingClient() (*opensearchapi.Client, error) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		w.WriteHeader(http.StatusBadRequest)
		io.Copy(w, strings.NewReader(`{"status": 400, "error": "Test Failing Client Response"}`))
	}))

	return opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Addresses: []string{ts.URL}}})
}

// VerifyInspect validates the returned opensearchapi.Inspect type
func VerifyInspect(t *testing.T, inspect opensearchapi.Inspect) {
	t.Helper()
	assert.NotEmpty(t, inspect)
	assert.Equal(t, http.StatusBadRequest, inspect.Response.StatusCode)
	assert.NotEmpty(t, inspect.Response.Body)
}

// SkipIfBelowVersion skips a test if the cluster version is below a given version
func SkipIfBelowVersion(t *testing.T, client *opensearchapi.Client, majorVersion, patchVersion int64, testName string) {
	t.Helper()
	resp, err := client.Info(context.Background(), nil)
	assert.Nil(t, err)
	major, patch, _, err := opensearch.ParseVersion(resp.Version.Number)
	assert.Nil(t, err)
	if major <= majorVersion && patch <= patchVersion {
		t.Skipf("Skiping %s as version %d.%d.x does not support this endpoint", testName, major, patch)
	}
}

// CompareRawJSONwithParsedJSON is a helper function to determin the difference between the parsed JSON and the raw JSON
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
			if operation.Type != "add" {
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

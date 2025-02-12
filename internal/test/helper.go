// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ostest

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wI2L/jsondiff"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

// NewClient returns an opensearchapi.Client that is adjusted for the wanted test case
func NewClient() (*opensearchapi.Client, error) {
	config, err := ClientConfig()
	if err != nil {
		return nil, err
	}
	if config != nil {
		return opensearchapi.NewClient(*config)
	}
	return opensearchapi.NewDefaultClient()
}

// IsSecure returns true when SECURE_INTEGRATION env is set to true
func IsSecure() bool {
	//nolint:gosimple // Getenv returns string not bool, if clause is needed
	if os.Getenv("SECURE_INTEGRATION") == "true" {
		return true
	}
	return false
}

// ClientConfig returns an opensearchapi.Config for secure opensearch
func ClientConfig() (*opensearchapi.Config, error) {
	if IsSecure() {
		password, err := GetPassword()
		if err != nil {
			return nil, err
		}

		return &opensearchapi.Config{
			Client: opensearch.Config{
				Username:  "admin",
				Password:  password,
				Addresses: []string{"https://localhost:9200"},
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				},
			},
		}, nil
	}
	//nolint:nilnil // easier to test with nil rather then doing complex error handling for tests
	return nil, nil
}

// GetPassword returns the password suited for the opensearch version
func GetPassword() (string, error) {
	var (
		major, minor int64
		err          error
	)
	password := "admin"
	version := os.Getenv("OPENSEARCH_VERSION")

	if version != "latest" && version != "" {
		major, minor, _, err = opensearch.ParseVersion(version)
		if err != nil {
			return "", err
		}
		if version == "latest" || major > 2 || (major == 2 && minor >= 12) {
			password = "myStrongPassword123!"
		}
	} else {
		password = "myStrongPassword123!"
	}
	return password, nil
}

// GetVersion gets cluster info and returns version as int's
func GetVersion(client *opensearchapi.Client) (int64, int64, int64, error) {
	resp, _, err := client.Info(context.Background(), nil)
	if err != nil {
		return 0, 0, 0, err
	}
	return opensearch.ParseVersion(resp.Version.Number)
}

// SkipIfBelowVersion skips a test if the cluster version is below a given version
func SkipIfBelowVersion(t *testing.T, client *opensearchapi.Client, majorVersion, patchVersion int64, testName string) {
	t.Helper()
	major, patch, _, err := GetVersion(client)
	assert.Nil(t, err)
	if major < majorVersion || (major == majorVersion && patch < patchVersion) {
		t.Skipf("Skiping %s as version %d.%d.x does not support this endpoint", testName, major, patch)
	}
}

// SkipIfNotSecure skips a test runs against an unsecure cluster
func SkipIfNotSecure(t *testing.T) {
	t.Helper()
	if !IsSecure() {
		t.Skipf("Skiping %s as it needs a secured cluster", t.Name())
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

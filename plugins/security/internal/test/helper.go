// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ossectest

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/opensearch-project/opensearch-go/v4"
	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
)

// NewClient returns an opensearchapi.Client that is adjusted for the wanted test case
func NewClient() (*security.Client, error) {
	config, err := ClientConfig()
	if err != nil {
		return nil, err
	}
	if config == nil {
		return nil, fmt.Errorf("failed to get config: requires secure opensearch")
	}
	return security.NewClient(*config)
}

// ClientConfig returns an opensearchapi.Config for secure opensearch
func ClientConfig() (*security.Config, error) {
	if ostest.IsSecure() {
		password, err := ostest.GetPassword()
		if err != nil {
			return nil, err
		}

		return &security.Config{
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

// CreateFailingClient returns an security.Client that always return 400 with an empty object as body
func CreateFailingClient() (*security.Client, error) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		w.WriteHeader(http.StatusBadRequest)
		io.Copy(w, strings.NewReader(`{"status": "error", "reason": "Test Failing Client Response"}`))
	}))

	return security.NewClient(security.Config{Client: opensearch.Config{Addresses: []string{ts.URL}}})
}

// VerifyResponse validates the returned security.Inspect type
func VerifyResponse(t *testing.T, response *opensearch.Response) {
	t.Helper()
	assert.NotEmpty(t, response)
	assert.Equal(t, http.StatusBadRequest, response.StatusCode)
	assert.NotEmpty(t, response.Body)
}

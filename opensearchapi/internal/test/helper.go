// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osapitest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

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

// VerifyResponse validates the returned opensearchapi.Response type
func VerifyResponse(t *testing.T, response *opensearch.Response) {
	t.Helper()
	assert.NotNil(t, response)
	assert.Equal(t, http.StatusBadRequest, response.StatusCode)
	assert.NotEmpty(t, response.Body)
}

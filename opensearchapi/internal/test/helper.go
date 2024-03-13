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

// DummyInspect is a struct to match the Response interface that is used for testing
type DummyInspect struct {
	Response *opensearch.Response
}

// Inspect is a fuction of DummyInspect use to match the Response interface
func (r DummyInspect) Inspect() opensearchapi.Inspect {
	return opensearchapi.Inspect{Response: r.Response}
}

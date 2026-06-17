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

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// Response is a dummy interface to run tests with Inspect()
type Response interface {
	Inspect() opensearchapi.Inspect
}

// CreateFailingClient returns an opensearchapi.Client that always returns 400 with an empty object as body.
func CreateFailingClient(t *testing.T) (*opensearchapi.Client, error) {
	t.Helper()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		w.WriteHeader(http.StatusBadRequest)
		io.Copy(w, strings.NewReader(`{"status": 400, "error": "Test Failing Client Response"}`))
	}))
	t.Cleanup(ts.Close)

	return opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: []string{ts.URL},
			Context:   t.Context(),
		},
	})
}

// VerifyInspect validates the returned opensearchapi.Inspect type
func VerifyInspect(t *testing.T, inspect opensearchapi.Inspect) {
	t.Helper()
	require.NotNil(t, inspect.Response, "Inspect().Response must not be nil")
	require.Equal(t, http.StatusBadRequest, inspect.Response.StatusCode)
	require.NotEmpty(t, inspect.Response.Body)
}

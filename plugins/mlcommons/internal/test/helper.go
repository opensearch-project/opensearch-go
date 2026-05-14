// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osmlcommonstest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v4/plugins/mlcommons"
)

// Response is a dummy interface to run tests with Inspect()
type Response interface {
	Inspect() mlcommons.Inspect
}

// NewClient returns an mlcommons.Client adjusted for the active test environment.
func NewClient(t *testing.T) (*mlcommons.Client, error) {
	t.Helper()
	config := ClientConfig(t)
	if config == nil {
		return mlcommons.NewClient(mlcommons.Config{
			Client: opensearch.Config{Context: t.Context()},
		})
	}
	return mlcommons.NewClient(*config)
}

// ClientConfig returns an mlcommons.Config for the secure or insecure local cluster.
// Returns nil when no SECURE_INTEGRATION setup is needed (the caller falls back to defaults).
func ClientConfig(t *testing.T) *mlcommons.Config {
	t.Helper()
	u := testutil.GetTestURL(t)

	if testutil.IsSecure(t) {
		password := testutil.GetPassword(t)

		return &mlcommons.Config{
			Client: opensearch.Config{
				Username:           "admin",
				Password:           password,
				Addresses:          []string{u.String()},
				Context:            t.Context(),
				InsecureSkipVerify: true,
			},
		}
	}

	return nil
}

// CreateFailingClient returns an mlcommons.Client backed by an httptest server that
// always responds 400 with a JSON error body. Useful for Inspect / error-path coverage.
func CreateFailingClient(t *testing.T) (*mlcommons.Client, error) {
	t.Helper()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		w.WriteHeader(http.StatusBadRequest)
		io.Copy(w, strings.NewReader(`{"status": "error", "reason": "Test Failing Client Response"}`))
	}))
	t.Cleanup(ts.Close)

	return mlcommons.NewClient(mlcommons.Config{
		Client: opensearch.Config{
			Addresses: []string{ts.URL},
			Context:   t.Context(),
		},
	})
}

// VerifyInspect validates the returned mlcommons.Inspect type.
func VerifyInspect(t *testing.T, inspect mlcommons.Inspect) {
	t.Helper()
	assert.NotEmpty(t, inspect)
	assert.Equal(t, http.StatusBadRequest, inspect.Response.StatusCode)
	assert.NotEmpty(t, inspect.Response.Body)
}

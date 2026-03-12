// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osismtest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v4/plugins/ism"
)

// Response is a dummy interface to run tests with Inspect()
type Response interface {
	Inspect() ism.Inspect
}

// NewClient returns an opensearchapi.Client that is adjusted for the wanted test case
func NewClient(t *testing.T) (*ism.Client, error) {
	t.Helper()
	config := ClientConfig(t)
	if config == nil {
		return ism.NewClient(ism.Config{
			Client: opensearch.Config{Context: t.Context()},
		})
	}
	return ism.NewClient(*config)
}

// ClientConfig returns an ism.Config for secure opensearch
func ClientConfig(t *testing.T) *ism.Config {
	t.Helper()
	// Use centralized URL construction
	u := testutil.GetTestURL(t)

	if testutil.IsSecure(t) {
		password := testutil.GetPassword(t)

		return &ism.Config{
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

// CreateFailingClient returns an ism.Client that always return 400 with an empty object as body.
// The httptest server is closed via t.Cleanup; background pollers are stopped automatically
// when t.Context() is cancelled at test end.
func CreateFailingClient(t *testing.T) (*ism.Client, error) {
	t.Helper()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		w.WriteHeader(http.StatusBadRequest)
		io.Copy(w, strings.NewReader(`{"status": "error", "reason": "Test Failing Client Response"}`))
	}))
	t.Cleanup(ts.Close)

	return ism.NewClient(ism.Config{
		Client: opensearch.Config{
			Addresses: []string{ts.URL},
			Context:   t.Context(),
		},
	})
}

// VerifyInspect validates the returned ism.Inspect type
func VerifyInspect(t *testing.T, inspect ism.Inspect) {
	t.Helper()
	assert.NotEmpty(t, inspect)
	assert.Equal(t, http.StatusBadRequest, inspect.Response.StatusCode)
	assert.NotEmpty(t, inspect.Response.Body)
}

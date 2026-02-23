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
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
)

// Response is a dummy interface to run tests with Inspect()
type Response interface {
	Inspect() security.Inspect
}

// NewClient returns an opensearchapi.Client that is adjusted for the wanted test case
func NewClient(t *testing.T) (*security.Client, error) {
	t.Helper()
	config := ClientConfig(t)
	if config == nil {
		return nil, fmt.Errorf("failed to get config: requires secure opensearch")
	}
	return security.NewClient(*config)
}

// ClientConfig returns a security.Config for secure opensearch
func ClientConfig(t *testing.T) *security.Config {
	t.Helper()
	// Use centralized URL construction
	u := testutil.GetTestURL(t)

	if testutil.IsSecure(t) {
		password := testutil.GetPassword(t)

		return &security.Config{
			Client: opensearch.Config{
				Username:  "admin",
				Password:  password,
				Addresses: []string{u.String()},
				Context:   t.Context(),
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- Test environment only
				},
			},
		}
	}

	return nil
}

// CreateFailingClient returns a security.Client that always return 400 with an empty object as body.
// The httptest server is closed via t.Cleanup; background pollers are stopped automatically
// when t.Context() is cancelled at test end.
func CreateFailingClient(t *testing.T) (*security.Client, error) {
	t.Helper()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		w.WriteHeader(http.StatusBadRequest)
		io.Copy(w, strings.NewReader(`{"status": "error", "reason": "Test Failing Client Response"}`))
	}))
	t.Cleanup(ts.Close)

	return security.NewClient(security.Config{
		Client: opensearch.Config{
			Addresses: []string{ts.URL},
			Context:   t.Context(),
		},
	})
}

// VerifyInspect validates the returned security.Inspect type
func VerifyInspect(t *testing.T, inspect security.Inspect) {
	t.Helper()
	assert.NotEmpty(t, inspect)
	assert.Equal(t, http.StatusBadRequest, inspect.Response.StatusCode)
	assert.NotEmpty(t, inspect.Response.Body)
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osismtest

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil/mockhttp"
	"github.com/opensearch-project/opensearch-go/v4/plugins/ism"
)

// Response is a dummy interface to run tests with Inspect()
type Response interface {
	Inspect() ism.Inspect
}

// NewClient returns an opensearchapi.Client that is adjusted for the wanted test case
func NewClient(t *testing.T) (*ism.Client, error) {
	t.Helper()
	config, err := ClientConfig(t)
	if err != nil {
		return nil, err
	}
	if config == nil {
		return ism.NewClient(ism.Config{})
	}
	return ism.NewClient(*config)
}

// ClientConfig returns an opensearchapi.Config for secure opensearch
func ClientConfig(t *testing.T) (*ism.Config, error) {
	t.Helper()
	// Use centralized URL construction
	u := mockhttp.GetOpenSearchURL(t)

	if testutil.IsSecure(t) {
		password, err := testutil.GetPassword(t)
		if err != nil {
			return nil, err
		}

		return &ism.Config{
			Client: opensearch.Config{
				Username:  "admin",
				Password:  password,
				Addresses: []string{u.String()},
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- Test environment only
				},
			},
		}, nil
	}
	//nolint:nilnil // easier to test with nil rather then doing complex error handling for tests
	return nil, nil
}

// CreateFailingClient returns an ism.Client that always return 400 with an empty object as body
func CreateFailingClient() (*ism.Client, error) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		w.WriteHeader(http.StatusBadRequest)
		io.Copy(w, strings.NewReader(`{"status": "error", "reason": "Test Failing Client Response"}`))
	}))

	return ism.NewClient(ism.Config{Client: opensearch.Config{Addresses: []string{ts.URL}}})
}

// VerifyInspect validates the returned ism.Inspect type
func VerifyInspect(t *testing.T, inspect ism.Inspect) {
	t.Helper()
	assert.NotEmpty(t, inspect)
	assert.Equal(t, http.StatusBadRequest, inspect.Response.StatusCode)
	assert.NotEmpty(t, inspect.Response.Body)
}

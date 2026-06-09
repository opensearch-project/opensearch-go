// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ossectest

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v5/plugins/security"
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

// SkipPreWriteRaceVersion skips a security write test on OpenSearch < 2.2.0.
//
// Those releases have a non-thread-safe User serialization race
// (java.io.OptionalDataException) during inter-node transport, which surfaces
// as a 500 on any write to the .opendistro_security index. Fixed in 2.2.0 by
// opensearch-project/security#1970. Read-only security tests are unaffected and
// should not call this.
func SkipPreWriteRaceVersion(t *testing.T) {
	t.Helper()
	apiClient, err := testutil.NewClient(t)
	require.NoError(t, err)
	testutil.SkipIfVersion(t, apiClient, "<", "2.2.0", "security plugin OptionalDataException (opensearch-project/security#1970)")
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
	require.NotEmpty(t, inspect)
	if inspect.Response == nil {
		return
	}
	require.Equal(t, http.StatusBadRequest, inspect.Response.StatusCode)
	require.NotEmpty(t, inspect.Response.Body)
}

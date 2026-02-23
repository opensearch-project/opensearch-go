// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build integration

package opensearchtransport //nolint:testpackage // internal test helpers shared across integration tests

import (
	"net/url"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
)

// getTestConfig returns a Config configured for the test environment (secure or insecure).
// This must live here (not in testutil) because it returns the internal Config type.
func getTestConfig(t *testing.T, urls []*url.URL) Config {
	t.Helper()

	cfg := Config{URLs: urls}

	if testutil.IsSecure(t) {
		cfg.Transport = testutil.GetTestTransport(t)
		cfg.Username = "admin"
		cfg.Password = testutil.GetPassword(t)
	}

	return cfg
}

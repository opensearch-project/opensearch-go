// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build integration

package opensearchtransport

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"testing"

	"golang.org/x/mod/semver"

	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil/mockhttp"
)

const (
	adminPasswordDefault = "admin"
	adminPasswordStrong  = "myStrongPassword123!"
)

// getTestTransport returns an http.RoundTripper configured for secure or insecure mode.
// This helper is used across opensearchtransport integration tests to avoid duplication.
// Defaults to insecure to match Makefile and dev environments.
func getTestTransport(t *testing.T) http.RoundTripper {
	t.Helper()
	val, found := os.LookupEnv("SECURE_INTEGRATION")
	if !found {
		return http.DefaultTransport // Default to insecure
	}
	isSecure, err := strconv.ParseBool(val)
	if err != nil {
		return http.DefaultTransport // Default to insecure on parse error
	}
	if isSecure {
		return &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- Test environment only
		}
	}
	return http.DefaultTransport
}

// getTestPassword returns the appropriate admin password based on OpenSearch version.
// This matches the logic in opensearchutil/testutil.GetPassword but avoids import cycles.
// OpenSearch 2.12.0+ changed the default admin password from "admin" to "myStrongPassword123!".
func getTestPassword(t *testing.T) string {
	t.Helper()
	version := os.Getenv("OPENSEARCH_VERSION")

	// Default to pre-2.12 password for empty version
	if version == "" {
		return adminPasswordDefault
	}

	if version == "latest" {
		// Latest uses the post-2.12 default password
		return adminPasswordStrong
	}

	// Normalize version to semver format (v2.12.0)
	if version[0] != 'v' {
		version = "v" + version
	}

	// Validate semver format
	if !semver.IsValid(version) {
		t.Logf("Invalid version format: %s, using default password", version)
		return adminPasswordDefault
	}

	// OpenSearch 2.12.0+ uses the new default password
	if semver.Compare(version, "v2.12.0") >= 0 {
		return adminPasswordStrong
	}

	return adminPasswordDefault
}

// getTestConfig returns a Config configured for the test environment (secure or insecure).
// This helper centralizes the logic for setting up test configurations across opensearchtransport tests.
// It avoids import cycles with opensearchutil/testutil by implementing the logic inline.
func getTestConfig(t *testing.T, urls []*url.URL) Config {
	t.Helper()

	cfg := Config{URLs: urls}

	// Check if we're in secure mode
	if len(urls) > 0 && urls[0].Scheme == "https" {
		cfg.Transport = getTestTransport(t)
		cfg.Username = "admin"
		cfg.Password = getTestPassword(t)
	}

	return cfg
}

// getTestURL returns the OpenSearch URL for testing, properly configured for secure/insecure mode.
// This is a convenience wrapper around mockhttp.GetOpenSearchURL.
func getTestURL(t *testing.T) *url.URL {
	t.Helper()
	return mockhttp.GetOpenSearchURL(t)
}

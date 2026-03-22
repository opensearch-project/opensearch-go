// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package testutil

import (
	"net/http"
	"net/url"
	"os"
	"strconv"
	"testing"

	"golang.org/x/mod/semver"
)

const (
	passwordDefault = "admin"
	passwordStrong  = "myStrongPassword123!"
)

// IsSecure returns true when SECURE_INTEGRATION=true.
// This is the canonical implementation -- all other packages should delegate here.
func IsSecure(t *testing.T) bool {
	t.Helper()
	val, found := os.LookupEnv("SECURE_INTEGRATION")
	if !found {
		return false // Default to insecure to match Makefile and dev environments
	}
	secure, err := strconv.ParseBool(val)
	if err != nil {
		return false // Default to insecure on parse error
	}
	return secure
}

// GetScheme returns "http" or "https" based on SECURE_INTEGRATION setting.
func GetScheme(t *testing.T) string {
	t.Helper()
	if IsSecure(t) {
		return "https"
	}
	return "http"
}

// GetPassword returns the appropriate admin password based on OPENSEARCH_VERSION.
// OpenSearch 2.12.0+ changed the default admin password from "admin" to "myStrongPassword123!".
// When OPENSEARCH_VERSION is unset, defaults to the strong password since all modern clusters
// (2.12.0+) require it and the old "admin" password would cause silent auth failures.
func GetPassword(t *testing.T) string {
	t.Helper()
	version := os.Getenv("OPENSEARCH_VERSION")

	if version == "" || version == "latest" {
		return passwordStrong
	}

	// Normalize version to semver format (v2.12.0)
	if version[0] != 'v' {
		version = "v" + version
	}

	if !semver.IsValid(version) {
		t.Logf("GetPassword: invalid version format: %s, using strong password", version)
		return passwordStrong
	}

	if semver.Compare(version, "v2.12.0") >= 0 {
		return passwordStrong
	}

	return passwordDefault
}

// GetTestURL returns the OpenSearch URL for testing.
// Checks OPENSEARCH_URL env var first, then falls back to localhost:9200 with
// the scheme determined by SECURE_INTEGRATION.
func GetTestURL(t *testing.T) *url.URL {
	t.Helper()

	if envURL, exists := os.LookupEnv("OPENSEARCH_URL"); exists && envURL != "" {
		u, err := url.Parse(envURL)
		if err != nil {
			t.Fatalf("GetTestURL: invalid OPENSEARCH_URL %q: %v", envURL, err)
		}
		return u
	}

	return &url.URL{Scheme: GetScheme(t), Host: "localhost:9200"}
}

// GetTestTransport returns an http.RoundTripper configured for the test environment.
// Returns a TLS-skipping transport when SECURE_INTEGRATION=true, otherwise http.DefaultTransport.
// When cloning, all DefaultTransport defaults (connection pooling, HTTP/2, timeouts) are preserved.
func GetTestTransport(t *testing.T) http.RoundTripper {
	t.Helper()
	if IsSecure(t) {
		tp := http.DefaultTransport.(*http.Transport).Clone()
		tp.TLSClientConfig.InsecureSkipVerify = true // #nosec G402 -- Test environment only
		return tp
	}
	return http.DefaultTransport
}

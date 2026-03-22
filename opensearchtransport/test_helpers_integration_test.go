// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchtransport)

package opensearchtransport_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
)

// Test timeout constants - much faster than production for quicker test runs
const (
	testDiscoverNodesInterval        = 100 * time.Millisecond
	testResurrectTimeoutInitial      = 100 * time.Millisecond
	testResurrectTimeoutMax          = 1 * time.Second
	testResurrectTimeoutFactorCutoff = 3
	testMinimumResurrectTimeout      = 50 * time.Millisecond
	testJitterScale                  = 0.1
	testHealthCheckTimeout           = 500 * time.Millisecond
	testHealthCheckMaxRetries        = 2
	testHealthCheckJitter            = 0.05
	testDiscoveryHealthCheckRetries  = 1
)

// testURLs returns the OpenSearch seed URLs for integration tests, configured for
// secure or insecure mode via the SECURE_INTEGRATION environment variable.
func testURLs(t *testing.T) []*url.URL {
	t.Helper()
	baseURL := testutil.GetTestURL(t)
	return []*url.URL{
		{Scheme: baseURL.Scheme, Host: "localhost:9200"},
		{Scheme: baseURL.Scheme, Host: "localhost:9201"},
	}
}

// testTimeoutsConfig returns a Config with only timeouts optimized for testing.
// Does NOT set DiscoverNodesInterval -- auto-discovery is disabled by default.
// Tests that need discovery should call transport.DiscoverNodes() explicitly.
// These values are much shorter than production defaults to make tests run faster.
func testTimeoutsConfig() opensearchtransport.Config {
	return opensearchtransport.Config{
		// Fast resurrection for dead connections
		ResurrectTimeoutInitial:      testResurrectTimeoutInitial,      // vs 5s in production
		ResurrectTimeoutMax:          testResurrectTimeoutMax,          // vs 30s in production
		ResurrectTimeoutFactorCutoff: testResurrectTimeoutFactorCutoff, // vs 5 in production
		MinimumResurrectTimeout:      testMinimumResurrectTimeout,      // vs 500ms in production
		JitterScale:                  testJitterScale,                  // vs 0.5 in production

		// Fast health checks
		HealthCheckTimeout:    testHealthCheckTimeout,    // vs 5s in production
		HealthCheckMaxRetries: testHealthCheckMaxRetries, // vs 6 in production
		HealthCheckJitter:     testHealthCheckJitter,     // vs 0.1 in production

		// Fast discovery health checks
		DiscoveryHealthCheckRetries: testDiscoveryHealthCheckRetries, // vs 3 in production
	}
}

// testConfigWithAuth returns a Config with URLs, credentials, TLS, and fast timeouts
// configured for the current test environment (secure or insecure).
// Auto-discovery is disabled; call transport.DiscoverNodes() explicitly.
func testConfigWithAuth(t *testing.T) opensearchtransport.Config {
	t.Helper()
	cfg := testTimeoutsConfig()
	cfg.URLs = testURLs(t)
	if testutil.IsSecure(t) {
		cfg.Username = "admin"
		cfg.Password = testutil.GetPassword(t)
		cfg.Transport = testutil.GetTestTransport(t)
	}
	return cfg
}

// mustRolePolicy creates a RolePolicy or panics on error.
// Only for use in tests where setup errors should fail fast.
func mustRolePolicy(role string) opensearchtransport.Policy {
	policy, err := opensearchtransport.NewRolePolicy(role)
	if err != nil {
		panic(err)
	}
	return policy
}

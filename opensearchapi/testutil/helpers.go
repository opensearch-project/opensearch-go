// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package testutil provides test helpers that depend on opensearchapi and opensearch types.
// For environment helpers (IsSecure, GetTestURL, etc.), use opensearchtransport/testutil.
package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wI2L/jsondiff"
	"golang.org/x/mod/semver"
	"golang.org/x/sync/semaphore"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	tptestutil "github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
)

// readinessSem limits how many goroutines can run WaitForClusterReady
// concurrently. Each attempt issues Cluster.State and Nodes.Info, which are
// heavy operations on a shared test cluster.
//
//nolint:gochecknoglobals // test-only process-level throttle
var readinessSem = semaphore.NewWeighted(2)

// sharedClient is a per-package singleton that avoids redundant cluster
// readiness checks. Created once via sync.Once with a background context
// so it outlives any individual test function.
//
//nolint:gochecknoglobals // test-only process-level singleton
var (
	sharedClient     *opensearchapi.Client
	sharedClientOnce sync.Once
	errSharedClient  error
)

// Re-export commonly used helpers from opensearchtransport/testutil so that
// most consumer test files only need a single testutil import.
//
//nolint:gochecknoglobals // re-exported test helpers
var (
	// MustUniqueString returns a unique string with the given prefix.
	MustUniqueString = tptestutil.MustUniqueString

	// IsDebugEnabled returns true when OPENSEARCH_GO_DEBUG is set.
	IsDebugEnabled = tptestutil.IsDebugEnabled

	// IsSecure returns true when SECURE_INTEGRATION is set.
	IsSecure = tptestutil.IsSecure

	// GetTestURL returns the OpenSearch URL for testing based on environment variables.
	GetTestURL = tptestutil.GetTestURL

	// GetPassword returns the admin password for testing.
	GetPassword = tptestutil.GetPassword

	// GetTestTransport returns an http.RoundTripper configured for the test environment.
	GetTestTransport = tptestutil.GetTestTransport

	// SkipIfNotSecure skips a test that runs against an insecure cluster.
	SkipIfNotSecure = tptestutil.SkipIfNotSecure

	// RequireMinNodes polls until the cluster has at least minNodes nodes, or skips.
	RequireMinNodes = tptestutil.RequireMinNodes

	// ShouldIgnoreField returns true if a JSON path is known to be dynamic/optional.
	ShouldIgnoreField = tptestutil.ShouldIgnoreField

	// PollUntil repeatedly calls checkFn until it returns true or the context times out.
	PollUntil = tptestutil.PollUntil
)

// ClientConfig returns an opensearchapi.Config for both secure and insecure opensearch
func ClientConfig(t *testing.T) *opensearchapi.Config {
	t.Helper()

	cfg := &opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: []string{tptestutil.GetTestURL(t).String()},
			Context:   t.Context(),
		},
	}

	if tptestutil.IsSecure(t) {
		cfg.Client.Username = "admin"
		cfg.Client.Password = tptestutil.GetPassword(t)
		cfg.Client.Transport = tptestutil.GetTestTransport(t)
	}

	return cfg
}

// GetVersion gets cluster info and returns version as int's
func GetVersion(t *testing.T, ctx context.Context, client *opensearchapi.Client) (int64, int64, int64, error) {
	t.Helper()
	if client == nil {
		return 0, 0, 0, fmt.Errorf("client cannot be nil")
	}
	resp, err := client.Info(ctx, nil)
	if err != nil {
		return 0, 0, 0, err
	}
	return opensearch.ParseVersion(resp.Version.Number)
}

// SkipIfVersion skips a test when the cluster version satisfies the given
// operator and version constraint.
//
// Examples:
//
//	SkipIfVersion(t, client, "<",  "2.15",   "Point_In_Time")  // skip below 2.15.0
//	SkipIfVersion(t, client, "=",  "2.15",   "Audit API")      // skip on 2.15.x (any patch)
//	SkipIfVersion(t, client, ">=", "3.0",    "NewFeature")      // skip on 3.0.0+
//	SkipIfVersion(t, client, "=",  "2.15.0", "ExactMatch")      // skip on exactly 2.15.0
//
// Supported operators: =, !=, <, <=, >, >=
//
// When the version has no patch component (e.g. "2.15"), the = and !=
// operators match on major.minor only (any patch). All other operators
// treat the missing patch as 0.
func SkipIfVersion(t *testing.T, client *opensearchapi.Client, operator string, version string, testName string) {
	t.Helper()
	sMajor, sMinor, sPatch, err := GetVersion(t, t.Context(), client)
	require.NoError(t, err)

	cMajor, cMinor, cPatch, hasPatch := parseVersion(t, version)

	serverSemver := fmt.Sprintf("v%d.%d.%d", sMajor, sMinor, sPatch)
	targetSemver := fmt.Sprintf("v%d.%d.%d", cMajor, cMinor, cPatch)

	var matches bool
	switch {
	case operator == "=" && !hasPatch:
		matches = sMajor == cMajor && sMinor == cMinor
	case operator == "!=" && !hasPatch:
		matches = sMajor != cMajor || sMinor != cMinor
	default:
		cmp := semver.Compare(serverSemver, targetSemver)
		switch operator {
		case "=":
			matches = cmp == 0
		case "!=":
			matches = cmp != 0
		case "<":
			matches = cmp < 0
		case "<=":
			matches = cmp <= 0
		case ">":
			matches = cmp > 0
		case ">=":
			matches = cmp >= 0
		default:
			t.Fatalf("SkipIfVersion: unsupported operator %q: must be =, !=, <, <=, >, or >=", operator)
		}
	}

	if matches {
		t.Skipf("Skipping %s: server version %s matches constraint %q %s", testName, serverSemver[1:], operator, version)
	}
}

// parseVersion parses a version string like "2.15" or "2.4.0" into its
// components. When the patch is absent, hasPatch is false and patch is 0.
func parseVersion(t *testing.T, version string) (int64, int64, int64, bool) {
	t.Helper()

	var major, minor, patch int64
	var hasPatch bool

	parts := strings.Split(version, ".")
	switch len(parts) {
	case 2:
		var err error
		major, err = strconv.ParseInt(parts[0], 10, 64)
		require.NoError(t, err, "SkipIfVersion: invalid major version in %q", version)
		minor, err = strconv.ParseInt(parts[1], 10, 64)
		require.NoError(t, err, "SkipIfVersion: invalid minor version in %q", version)
	case 3:
		hasPatch = true
		var err error
		major, err = strconv.ParseInt(parts[0], 10, 64)
		require.NoError(t, err, "SkipIfVersion: invalid major version in %q", version)
		minor, err = strconv.ParseInt(parts[1], 10, 64)
		require.NoError(t, err, "SkipIfVersion: invalid minor version in %q", version)
		patch, err = strconv.ParseInt(parts[2], 10, 64)
		require.NoError(t, err, "SkipIfVersion: invalid patch version in %q", version)
	default:
		t.Fatalf("SkipIfVersion: version %q must be major.minor or major.minor.patch", version)
	}

	return major, minor, patch, hasPatch
}

// ExtendedReadinessCheck performs validation checks to ensure the
// cluster is ready for complex operations
func ExtendedReadinessCheck(t *testing.T, ctx context.Context, client *opensearchapi.Client) error {
	t.Helper()

	// Try a simple cluster state request - this exercises more Java serialization paths
	_, err := client.Cluster.State(ctx, nil)
	if err != nil {
		return fmt.Errorf("cluster state check failed: %w", err)
	}

	// Try a simple nodes info request - exercises node-level serialization
	_, err = client.Nodes.Info(ctx, nil)
	if err != nil {
		return fmt.Errorf("nodes info check failed: %w", err)
	}

	return nil
}

// NewClient returns a shared opensearchapi.Client that is safe for concurrent
// use across tests within the same package. The client is created once with
// full cluster readiness checking; subsequent calls return immediately.
//
// The shared client uses a background context for its transport so that
// background goroutines (discovery, health checks) outlive any individual
// test. API calls should pass their own context (e.g. t.Context()).
//
// Use InitClient instead when a test needs a dedicated client with custom
// configuration or needs to test client lifecycle behavior.
func NewClient(t *testing.T) (*opensearchapi.Client, error) {
	t.Helper()
	sharedClientOnce.Do(func() {
		config := ClientConfig(t)
		// Use background context: the shared client must outlive any
		// individual test. Each API call passes its own ctx.
		config.Client.Context = context.Background()

		var err error
		sharedClient, err = opensearchapi.NewClient(*config)
		if err != nil {
			errSharedClient = err
			return
		}

		errSharedClient = WaitForClusterReady(t, sharedClient)
	})
	if errSharedClient != nil {
		return nil, fmt.Errorf("shared client initialization failed: %w", errSharedClient)
	}
	return sharedClient, nil
}

// InitClient creates a new opensearchapi.Client with full cluster readiness
// checking. Each call creates a fresh client, making this suitable for tests
// that need custom transport configuration, test client lifecycle behavior
// (e.g., connection management, standby rotation), or perform heavy operations
// that benefit from a dedicated readiness check.
//
// Most tests should use NewClient instead for better performance.
func InitClient(t *testing.T) (*opensearchapi.Client, error) {
	t.Helper()
	config := ClientConfig(t)

	client, err := opensearchapi.NewClient(*config)
	if err != nil {
		return nil, err
	}

	// Always wait for cluster readiness
	err = WaitForClusterReady(t, client)
	if err != nil {
		return nil, err
	}

	return client, nil
}

// classifyConnError inspects a connection error and returns a fatal error if
// the caller should stop retrying, or nil if retries should continue.
// It tracks consecutive EOF counts via eofCount and only treats EOFs as fatal
// when everConnected is false (i.e. we have never successfully connected).
func classifyConnError(err error, everConnected bool, eofCount *int) error {
	errMsg := err.Error()

	// Detect authentication failures and fail fast -- retrying won't help.
	if strings.Contains(errMsg, "invalid character 'U'") ||
		strings.Contains(errMsg, "Unauthorized") {
		return fmt.Errorf("cluster returned Unauthorized (SECURE_INTEGRATION=%s, OPENSEARCH_VERSION=%s); "+
			"verify credentials are correct -- the admin password changed in OpenSearch 2.12.0+: %w",
			os.Getenv("SECURE_INTEGRATION"), os.Getenv("OPENSEARCH_VERSION"), err)
	}

	// Only count consecutive EOFs for fast-fail when we have NEVER
	// successfully connected. Once we've connected at least once,
	// EOFs are transient (e.g. cluster disrupted by HotThreads or
	// other heavy operations) and should be retried normally.
	//
	// Use a threshold of 5 (not 3) because background pollers from
	// other test clients can cause brief EOF windows that a brand-new
	// client might hit during its first few connection attempts.
	if strings.Contains(errMsg, "EOF") && !everConnected {
		*eofCount++
		if *eofCount >= 5 {
			return fmt.Errorf("cluster returned EOF on %d consecutive attempts (SECURE_INTEGRATION=%s); "+
				"verify the cluster scheme matches this setting: %w",
				*eofCount, os.Getenv("SECURE_INTEGRATION"), err)
		}
	} else {
		*eofCount = 0
	}

	return nil
}

// WaitForClusterReady waits for the OpenSearch cluster to be fully ready for API calls.
// Retries connectivity, version detection, cluster health, and extended readiness checks.
// If all attempts fail with EOF, suggests checking the SECURE_INTEGRATION env var since
// this typically indicates an HTTP/HTTPS scheme mismatch.
//
// Concurrent calls are throttled by readinessSem so that at most 2 goroutines
// run the heavy readiness checks (Cluster.State, Nodes.Info) at the same time.
func WaitForClusterReady(t *testing.T, client *opensearchapi.Client) error {
	t.Helper()

	// Acquire semaphore slot; respect the test's context for cancellation.
	if err := readinessSem.Acquire(t.Context(), 1); err != nil {
		return fmt.Errorf("readiness semaphore acquire: %w", err)
	}
	defer readinessSem.Release(1)

	const (
		maxAttempts          = 25
		delayBetweenAttempts = 5 * time.Second
		requestTimeout       = 2 * time.Second
	)

	var (
		major, minor, patch int64
		eofCount            int
		everConnected       bool
	)

	for attempt := range maxAttempts {
		ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)

		// Get version (also serves as basic connectivity check).
		// Use the per-attempt ctx so the version fetch respects the same timeout.
		var err error
		major, minor, patch, err = GetVersion(t, ctx, client)
		if err != nil {
			cancel()

			if fatalErr := classifyConnError(err, everConnected, &eofCount); fatalErr != nil {
				return fatalErr
			}

			t.Logf("Waiting %s for cluster readiness (attempt %d/%d): %v", delayBetweenAttempts, attempt+1, maxAttempts, err)
			time.Sleep(delayBetweenAttempts)
			continue
		}
		eofCount = 0 // Reset on success
		everConnected = true

		// Basic cluster health check
		resp, err := client.Cluster.Health(ctx, nil)
		if err != nil || resp == nil {
			cancel()
			t.Logf("Waiting %s for cluster readiness (attempt %d/%d)...", delayBetweenAttempts, attempt+1, maxAttempts)
			time.Sleep(delayBetweenAttempts)
			continue
		}

		// Extended readiness validation
		if err := ExtendedReadinessCheck(t, ctx, client); err == nil {
			cancel()
			if attempt > 0 {
				t.Logf("Cluster ready after %d attempts (version %d.%d.%d)", attempt+1, major, minor, patch)
			}
			return nil
		}

		cancel()
		t.Logf("Cluster health OK but readiness validation failed (attempt %d/%d)", attempt+1, maxAttempts)
		time.Sleep(delayBetweenAttempts)
	}

	return fmt.Errorf("cluster not ready after %d attempts (version %d.%d.%d)", maxAttempts, major, minor, patch)
}

// CompareRawJSONwithParsedJSON is a helper function to determine the difference between the parsed JSON and the raw JSON
// this is helpful to detect missing fields in the go structs
func CompareRawJSONwithParsedJSON(t *testing.T, resp any, rawResp *opensearch.Response) {
	t.Helper()
	if _, ok := os.LookupEnv("OPENSEARCH_GO_SKIP_JSON_COMPARE"); ok {
		return
	}
	require.NotNil(t, rawResp)

	parsedBody, err := json.Marshal(resp)
	require.NoError(t, err)

	body, err := io.ReadAll(rawResp.Body)
	require.NoError(t, err)

	// If the parsedBody and body does not match, then we need to check if we are adding or removing fields
	if string(parsedBody) != string(body) {
		patch, err := jsondiff.CompareJSON(body, parsedBody)
		require.NoError(t, err)
		operations := make([]jsondiff.Operation, 0)
		for _, operation := range patch {
			// Ignore "add" operations (OpenSearch has extra fields we don't need)
			if operation.Type == "add" && operation.Path != "" {
				continue
			}

			// Ignore known dynamic/optional fields that shouldn't cause test failures
			if tptestutil.ShouldIgnoreField(t, operation.Path, "") {
				continue
			}

			operations = append(operations, operation)
		}
		assert.Empty(t, operations)
		if len(operations) == 0 {
			return
		}
		for _, op := range operations {
			fmt.Printf("%s\n", op)
		}
		fmt.Printf("%s\n", body)
	}
}

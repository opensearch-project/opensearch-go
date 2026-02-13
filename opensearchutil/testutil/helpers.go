// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package testutil provides utilities for testing OpenSearch Go client functionality.
// This package contains test helpers that are shared across integration tests
// and can be used by external test packages.
package testutil

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wI2L/jsondiff"
	"golang.org/x/mod/semver"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil/mockhttp"
)

// Common timeouts for testing
const (
	DefaultTestTimeout     = 30 * time.Second
	DefaultRequestTimeout  = 5 * time.Second
	DefaultPollingInterval = 100 * time.Millisecond
)

// MustParseURL parses a URL and panics if invalid (for test setup)
func MustParseURL(rawURL string) *url.URL {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(fmt.Sprintf("invalid URL %q: %v", rawURL, err))
	}
	return u
}

// MustUniqueString returns a unique string with the given prefix.
// This is useful for creating unique resource names in tests to avoid conflicts.
func MustUniqueString(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", prefix, rand.Int64()) // #nosec G404 -- Using math/rand for test resource names, not cryptographic purposes
}

// PollUntil repeatedly calls checkFn until it returns true or the context times out.
// It uses exponential backoff with jitter between attempts, based on the retry logic
// from opensearchtransport.backoffRetry().
//
// This is useful for waiting for eventual consistency in integration tests, such as
// waiting for ISM policies to be applied, indices to be ready, or cluster state changes.
//
// Parameters:
//   - t: testing.T for helper marking and logging
//   - ctx: context for timeout and cancellation control
//   - baseDelay: initial delay between attempts (e.g., 500ms)
//   - maxAttempts: maximum number of check attempts
//   - jitter: randomization factor (0.0-1.0) to avoid thundering herd
//   - checkFn: function that returns (ready bool, error). Returns true when condition is met.
//
// Example usage:
//
//	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
//	defer cancel()
//	err := testutil.PollUntil(t, ctx, 500*time.Millisecond, 10, 0.1, func() (bool, error) {
//	    resp, err := client.Explain(ctx, &ism.ExplainReq{Indices: indices})
//	    if err != nil {
//	        return false, err
//	    }
//	    return resp.Indices[index].Info != nil && resp.Indices[index].Info.Message != "", nil
//	})
func PollUntil(
	t *testing.T, ctx context.Context, baseDelay time.Duration,
	maxAttempts int, jitter float64, checkFn func() (bool, error),
) error {
	t.Helper()

	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	for attempt := range maxAttempts {
		// Check if context is already cancelled before attempting
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Call the check function
		ready, err := checkFn()
		if err != nil {
			return fmt.Errorf("check failed on attempt %d: %w", attempt+1, err)
		}
		if ready {
			return nil // Success
		}

		// If this is not the last attempt, wait before retrying
		if attempt < maxAttempts-1 && baseDelay > 0 {
			// Exponential backoff: base delay * 2^attempt
			// Cap attempt to prevent overflow (2^30 is ~1 billion, more than enough)
			cappedAttempt := min(attempt, 30)
			delay := time.Duration(int64(baseDelay) * (1 << cappedAttempt))

			// Apply jitter to avoid thundering herd
			// #nosec G404 -- Using math/rand for test retry jitter, not cryptographic purposes
			if jitter > 0.0 {
				jitterRange := float64(delay) * jitter
				jitterOffset := (rand.Float64()*2 - 1) * jitterRange // -jitter to +jitter
				delay = time.Duration(float64(delay) + jitterOffset)
			}

			// Wait with context cancellation support
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}

	return fmt.Errorf("condition not met after %d attempts", maxAttempts)
}

// IsSecure returns true when SECURE_INTEGRATION env is set to true.
// Defaults to false (insecure) when not set, matching the Makefile default
// and typical development environments.
func IsSecure(t *testing.T) bool {
	t.Helper()
	val, found := os.LookupEnv("SECURE_INTEGRATION")
	if !found {
		return false // Default to insecure to match Makefile and dev environments
	}
	isSecure, err := strconv.ParseBool(val)
	if err != nil {
		return false // Default to insecure on parse error
	}
	return isSecure
}

// GetScheme returns "http" or "https" based on SECURE_INTEGRATION setting.
// This centralizes the scheme determination logic for test consistency.
func GetScheme(t *testing.T) string {
	t.Helper()
	if IsSecure(t) {
		return "https"
	}
	return "http"
}

// ClientConfig returns an opensearchapi.Config for both secure and insecure opensearch
func ClientConfig(t *testing.T) (*opensearchapi.Config, error) {
	t.Helper()
	if !IsSecure(t) {
		// For insecure integration tests, use centralized URL construction
		return &opensearchapi.Config{
			Client: opensearch.Config{
				Addresses: []string{mockhttp.GetOpenSearchURL(t).String()},
			},
		}, nil
	}

	password, err := GetPassword(t)
	if err != nil {
		return nil, err
	}

	return &opensearchapi.Config{
		Client: opensearch.Config{
			Username:  "admin",
			Password:  password,
			Addresses: []string{mockhttp.GetOpenSearchURL(t).String()},
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- Intentionally skipping TLS verification for test environments
			},
		},
	}, nil
}

// GetPassword returns the password suited for the opensearch version
func GetPassword(t *testing.T) (string, error) {
	t.Helper()
	var (
		major, minor int64
		err          error
	)
	password := "admin"
	version := os.Getenv("OPENSEARCH_VERSION")

	if version != "latest" && version != "" {
		major, minor, _, err = opensearch.ParseVersion(version)
		if err != nil {
			return "", err
		}
		if version == "latest" || major > 2 || (major == 2 && minor >= 12) {
			password = "myStrongPassword123!"
		}
	} else {
		password = "myStrongPassword123!"
	}
	return password, nil
}

// GetVersion gets cluster info and returns version as int's
func GetVersion(t *testing.T, client *opensearchapi.Client) (int64, int64, int64, error) {
	t.Helper()
	if client == nil {
		return 0, 0, 0, fmt.Errorf("client cannot be nil")
	}
	resp, err := client.Info(t.Context(), nil)
	if err != nil {
		return 0, 0, 0, err
	}
	return opensearch.ParseVersion(resp.Version.Number)
}

// SkipIfBelowVersion skips a test if the cluster version is below a given version
func SkipIfBelowVersion(t *testing.T, client *opensearchapi.Client, majorVersion, patchVersion int64, testName string) {
	t.Helper()
	major, patch, _, err := GetVersion(t, client)
	require.NoError(t, err)
	if major < majorVersion || (major == majorVersion && patch < patchVersion) {
		t.Skipf("Skipping %s as version %d.%d.x does not support this endpoint", testName, major, patch)
	}
}

// SkipIfNotSecure skips a test that runs against an insecure cluster
func SkipIfNotSecure(t *testing.T) {
	t.Helper()
	if !IsSecure(t) {
		t.Skipf("Skipping %s as it needs a secured cluster", t.Name())
	}
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

// NewClient returns an opensearchapi.Client that is adjusted for the wanted test case
// and ensures the OpenSearch cluster is ready for requests.
func NewClient(t *testing.T) (*opensearchapi.Client, error) {
	t.Helper()
	config, err := ClientConfig(t)
	if err != nil {
		return nil, err
	}

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

// WaitForClusterReady waits for the OpenSearch cluster to be fully ready for API calls.
func WaitForClusterReady(t *testing.T, client *opensearchapi.Client) error {
	t.Helper()
	const (
		maxAttempts          = 25
		delayBetweenAttempts = 5 * time.Second
		requestTimeout       = 2 * time.Second
	)

	// Get version for informational logging
	major, minor, patch, err := GetVersion(t, client)
	if err != nil {
		return fmt.Errorf("failed to get OpenSearch version: %w", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	for attempt := range maxAttempts {
		// Basic cluster health check
		resp, err := client.Cluster.Health(ctx, nil)
		if err != nil || resp == nil {
			t.Logf("Waiting %s for cluster readiness (attempt %d/%d)...", delayBetweenAttempts, attempt+1, maxAttempts)
			time.Sleep(delayBetweenAttempts)

			// Reset context for next attempt
			ctx, cancel = context.WithTimeout(t.Context(), requestTimeout)
			defer cancel()
			continue
		}

		// Extended readiness validation
		if err := ExtendedReadinessCheck(t, ctx, client); err == nil {
			if attempt > 0 {
				t.Logf("Cluster ready after %d attempts (version %d.%d.%d)", attempt+1, major, minor, patch)
			}
			return nil
		}

		t.Logf("Cluster health OK but readiness validation failed (attempt %d/%d)", attempt+1, maxAttempts)
		time.Sleep(delayBetweenAttempts)

		// Reset context for next attempt
		ctx, cancel = context.WithTimeout(t.Context(), requestTimeout)
		defer cancel()
	}

	return fmt.Errorf("cluster not ready after %d attempts (version %d.%d.%d)", maxAttempts, major, minor, patch)
}

// ignoredFieldPatterns contains field patterns that should be ignored during JSON comparison
// Map of field patterns to their version requirements using operator+version syntax
// Format: {operator}{version}(,{operator}{version})*
// Examples: ">=v1.0.0", ">=v1.0.0,<v2.0.0", "=v2.1.0"
//
//nolint:gochecknoglobals // This is a test utility package
var ignoredFieldPatterns = map[string]string{
	// Dynamic IO statistics that change between calls - present in all supported versions
	"/io_stats/":         ">=v1.0.0",
	"/io_time_in_millis": ">=v1.0.0",
	"/queue_size":        ">=v1.0.0",
	"/read_time":         ">=v1.0.0",
	"/write_time":        ">=v1.0.0",

	// Optional script/template metadata - present in all supported versions
	"/options":                  ">=v1.0.0",
	"/metadata/stored_scripts/": ">=v1.0.0",

	// Dynamic cluster/node fields - present in all supported versions
	"/target_node":   ">=v1.0.0",
	"/relocation_id": ">=v1.0.0",

	// Dynamic task-related fields - present in all supported versions
	"/cancellation_time_millis": ">=v1.0.0",

	// Version-specific or environment-dependent fields
	"/build_flavor":   ">=v1.0.0",
	"/build_type":     ">=v1.0.0",
	"/build_snapshot": ">=v1.0.0",
	"/lucene_version": ">=v1.0.0",
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
			// TODO: Get actual server version instead of passing empty string
			// We could either:
			// 1. Modify function signature to accept version parameter
			// 2. Cache version in package variable during client creation
			// 3. Extract version from response headers if available
			if ShouldIgnoreField(t, operation.Path, "") {
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

// ShouldIgnoreField returns true if the given JSON path represents a field
// that is known to be dynamic, optional, or version-specific and should not
// cause test failures when missing from Go client structs.
func ShouldIgnoreField(t *testing.T, path string, serverVersion string) bool {
	t.Helper()
	for pattern, versionExpr := range ignoredFieldPatterns {
		if strings.Contains(path, pattern) {
			return EvaluateVersionExpression(t, serverVersion, versionExpr)
		}
	}
	return false
}

// EvaluateVersionExpression evaluates a version expression against a server version
// Format: {operator}{version}(,{operator}{version})*
// Examples: ">=v1.0.0", ">=v1.0.0,<v2.0.0", "=v2.1.0"
func EvaluateVersionExpression(t *testing.T, serverVersion, versionExpr string) bool {
	t.Helper()
	if versionExpr == "" {
		panic("empty version expression not allowed - use operator+version format like '>=v1.0.0'")
	}

	normalizedServerVersion := NormalizeVersion(t, serverVersion)
	if normalizedServerVersion == "" {
		// If we can't determine server version, be conservative and ignore the field
		// This handles cases where version detection fails
		return true
	}

	// Split by comma for multiple conditions (all must be true)
	conditions := strings.SplitSeq(versionExpr, ",")
	for condition := range conditions {
		condition = strings.TrimSpace(condition)
		if !EvaluateVersionCondition(t, normalizedServerVersion, condition) {
			return false
		}
	}
	return true
}

// EvaluateVersionCondition evaluates a single version condition
func EvaluateVersionCondition(t *testing.T, serverVersion, condition string) bool {
	t.Helper()
	// Parse operator and version from condition
	var operator, version string

	switch {
	case strings.HasPrefix(condition, ">="):
		operator = ">="
		version = condition[2:]
	case strings.HasPrefix(condition, "<="):
		operator = "<="
		version = condition[2:]
	case strings.HasPrefix(condition, ">"):
		operator = ">"
		version = condition[1:]
	case strings.HasPrefix(condition, "<"):
		operator = "<"
		version = condition[1:]
	case strings.HasPrefix(condition, "="):
		operator = "="
		version = condition[1:]
	default:
		panic(fmt.Sprintf("invalid version condition format: %q - use operator+version format like '>=v1.0.0'", condition))
	}

	normalizedVersion := NormalizeVersion(t, version)
	if normalizedVersion == "" {
		panic(fmt.Sprintf("invalid version format in condition: %q", condition))
	}

	comparison := semver.Compare(serverVersion, normalizedVersion)

	switch operator {
	case ">=":
		return comparison >= 0
	case "<=":
		return comparison <= 0
	case ">":
		return comparison > 0
	case "<":
		return comparison < 0
	case "=":
		return comparison == 0
	default:
		panic(fmt.Sprintf("unknown operator: %q", operator))
	}
}

// NormalizeVersion ensures the version string is in semver format (v1.2.3)
func NormalizeVersion(t *testing.T, version string) string {
	t.Helper()
	if version == "" {
		return ""
	}
	if !strings.HasPrefix(version, "v") {
		return "v" + version
	}
	return version
}

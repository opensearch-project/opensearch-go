// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/mod/semver"
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

// SkipIfNotSecure skips a test that runs against an insecure cluster
func SkipIfNotSecure(t *testing.T) {
	t.Helper()
	if !IsSecure(t) {
		t.Skipf("Skipping %s as it needs a secured cluster", t.Name())
	}
}

// SkipIfSingleNode skips a test if the cluster has fewer than minNodes nodes.
// Queries /_nodes/http to count nodes and skips if the cluster is too small.
// This is useful for tests that require multi-node features like standby rotation
// or role-based routing, which may run in CI against single-node clusters.
func SkipIfSingleNode(t *testing.T, minNodes int) {
	t.Helper()

	u := GetTestURL(t)
	nodesURL := *u
	nodesURL.Path = "/_nodes/http"

	client := &http.Client{
		Transport: GetTestTransport(t),
		Timeout:   5 * time.Second,
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, nodesURL.String(), nil)
	if err != nil {
		t.Fatalf("SkipIfSingleNode: failed to create request: %v", err)
	}
	if IsSecure(t) {
		req.SetBasicAuth("admin", GetPassword(t))
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("Skipping %s: cannot reach cluster to check node count: %v", t.Name(), err)
	}
	defer resp.Body.Close()

	// Parse just the _nodes.total field from the response
	var result struct {
		Nodes struct {
			Total int `json:"total"`
		} `json:"_nodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("SkipIfSingleNode: failed to decode /_nodes/http response: %v", err)
	}

	if result.Nodes.Total < minNodes {
		t.Skipf("Skipping %s: cluster has %d node(s), need at least %d", t.Name(), result.Nodes.Total, minNodes)
	}
}

// WaitForCluster waits for the OpenSearch cluster to be reachable and responding to HTTP requests.
// Unlike WaitForClusterReady in opensearchapi/testutil, this uses raw HTTP requests and does not
// require an opensearchapi.Client. This is useful for tests that need to wait for cluster
// availability before creating a transport.
func WaitForCluster(t *testing.T) {
	t.Helper()

	const (
		maxAttempts          = 25
		delayBetweenAttempts = 5 * time.Second
		requestTimeout       = 2 * time.Second
	)

	u := GetTestURL(t)
	healthURL := *u
	healthURL.Path = "/"

	client := &http.Client{Transport: GetTestTransport(t)}

	var eofCount int
	for attempt := range maxAttempts {
		ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL.String(), nil)
		if err != nil {
			cancel()
			t.Fatalf("WaitForCluster: failed to create request: %v", err)
		}

		if IsSecure(t) {
			req.SetBasicAuth("admin", GetPassword(t))
		}

		resp, err := client.Do(req)
		cancel()

		if err != nil {
			if strings.Contains(err.Error(), "EOF") {
				eofCount++
				if eofCount >= 3 {
					t.Fatalf("WaitForCluster: cluster returned EOF on %d consecutive attempts "+
						"(SECURE_INTEGRATION=%s); verify the cluster scheme matches this setting: %v",
						eofCount, os.Getenv("SECURE_INTEGRATION"), err)
				}
			} else {
				eofCount = 0
			}
			t.Logf("WaitForCluster: attempt %d/%d: %v", attempt+1, maxAttempts, err)
			time.Sleep(delayBetweenAttempts)
			continue
		}

		eofCount = 0
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			if attempt > 0 {
				t.Logf("WaitForCluster: cluster ready after %d attempts", attempt+1)
			}
			return
		}

		// Fail fast on authentication errors -- retrying with the same credentials won't help
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			t.Fatalf("WaitForCluster: cluster returned %d (SECURE_INTEGRATION=%s, OPENSEARCH_VERSION=%s); "+
				"verify credentials are correct -- the admin password changed in OpenSearch 2.12.0+",
				resp.StatusCode, os.Getenv("SECURE_INTEGRATION"), os.Getenv("OPENSEARCH_VERSION"))
		}

		t.Logf("WaitForCluster: attempt %d/%d: status %d", attempt+1, maxAttempts, resp.StatusCode)
		time.Sleep(delayBetweenAttempts)
	}

	t.Fatalf("WaitForCluster: cluster not ready after %d attempts (url=%s)", maxAttempts, healthURL.String())
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

// GetServerVersion queries the cluster root endpoint (GET /) and returns the
// server's version string in semver format (e.g., "v2.1.0"). This works at
// the transport level without requiring an opensearchapi.Client.
func GetServerVersion(t *testing.T) string {
	t.Helper()

	u := GetTestURL(t)
	rootURL := *u
	rootURL.Path = "/"

	client := &http.Client{
		Transport: GetTestTransport(t),
		Timeout:   5 * time.Second,
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, rootURL.String(), nil)
	if err != nil {
		t.Fatalf("GetServerVersion: failed to create request: %v", err)
	}
	if IsSecure(t) {
		req.SetBasicAuth("admin", GetPassword(t))
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GetServerVersion: failed to query cluster: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Version struct {
			Number string `json:"number"`
		} `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("GetServerVersion: failed to decode response: %v", err)
	}
	if result.Version.Number == "" {
		t.Fatal("GetServerVersion: server returned empty version number")
	}

	return NormalizeVersion(t, result.Version.Number)
}

// SkipIfVersion skips a test when the cluster version satisfies the given
// operator and version constraint. Queries the server directly via HTTP,
// so it can be used in transport-level tests without an opensearchapi.Client.
//
// Examples:
//
//	SkipIfVersion(t, "=",  "2.1.0", "shard-exact routing")  // skip on exactly 2.1.0
//	SkipIfVersion(t, "<",  "2.4",   "feature X")            // skip below 2.4.0
//	SkipIfVersion(t, "<=", "2.1",   "known server bug")     // skip on 2.1.x and below
//
// Supported operators: =, !=, <, <=, >, >=
//
// When the version has no patch component (e.g. "2.1"), the = and !=
// operators match on major.minor only (any patch). All other operators
// treat the missing patch as 0.
func SkipIfVersion(t *testing.T, operator string, version string, testName string) {
	t.Helper()

	serverVersion := GetServerVersion(t)

	// Parse the constraint version.
	constraintParts := strings.Split(version, ".")
	hasPatch := len(constraintParts) == 3
	constraintSemver := NormalizeVersion(t, version)
	if !hasPatch {
		constraintSemver = NormalizeVersion(t, version+".0")
	}

	var matches bool
	switch {
	case operator == "=" && !hasPatch:
		// Match on major.minor only (any patch).
		matches = semver.MajorMinor(serverVersion) == semver.MajorMinor(constraintSemver)
	case operator == "!=" && !hasPatch:
		matches = semver.MajorMinor(serverVersion) != semver.MajorMinor(constraintSemver)
	default:
		cmp := semver.Compare(serverVersion, constraintSemver)
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
		t.Skipf("Skipping %s: server version %s matches constraint %q %s",
			testName, serverVersion[1:], operator, version)
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

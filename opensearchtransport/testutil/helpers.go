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
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/mod/semver"

	"github.com/opensearch-project/opensearch-go/v5/internal/test/readiness"
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

// BackoffDelay returns the delay for the given attempt using exponential
// backoff with optional jitter. The formula is:
//
//	delay = baseDelay * 2^attempt ± (jitter * delay)
//
// jitter is a factor in [0.0, 1.0] that randomizes the delay symmetrically
// around the exponential value. A jitter of 0.0 returns the exact
// exponential delay; 0.5 returns a value in [delay*0.5, delay*1.5].
// The attempt is capped at 30 to prevent overflow.
func BackoffDelay(baseDelay time.Duration, attempt int, jitter float64) time.Duration {
	cappedAttempt := min(attempt, 30)
	delay := time.Duration(int64(baseDelay) * (1 << cappedAttempt))

	// #nosec G404 -- Using math/rand for test retry jitter, not cryptographic purposes
	if jitter > 0.0 {
		jitterRange := float64(delay) * jitter
		jitterOffset := (rand.Float64()*2 - 1) * jitterRange // -jitter to +jitter
		delay = time.Duration(float64(delay) + jitterOffset)
	}

	return delay
}

// PollUntil repeatedly calls checkFn until it returns true or the context times out.
// It uses exponential backoff with jitter between attempts via [BackoffDelay].
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
			delay := BackoffDelay(baseDelay, attempt, jitter)

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

// RequireMinNodes polls until the cluster has at least minNodes nodes, then
// returns. If the node count stabilizes below the threshold (same count on
// consecutive checks), the test is skipped immediately rather than burning
// the full timeout — this handles single-node CI clusters that will never
// grow.
//
// When the OPENSEARCH_NODE_COUNT environment variable is set (e.g., by CI or
// the Makefile), the function uses it as the expected cluster size:
//   - If the expected count < minNodes, the test is skipped immediately
//     with no network calls.
//   - Otherwise, the function polls until minNodes are present (the cluster
//     may still be forming).
//
// When OPENSEARCH_NODE_COUNT is unset, the function polls and uses stability
// detection (consecutive identical counts) to decide when to stop waiting.
func RequireMinNodes(t *testing.T, ctx context.Context, minNodes int) {
	t.Helper()

	// Fast path: if we know the cluster size from the environment, skip
	// immediately when it's too small.
	if envCount := os.Getenv("OPENSEARCH_NODE_COUNT"); envCount != "" {
		expected, err := strconv.Atoi(envCount)
		if err == nil && expected < minNodes {
			t.Skipf("Skipping %s: OPENSEARCH_NODE_COUNT=%d, need at least %d",
				t.Name(), expected, minNodes)
		}
	}

	u := GetTestURL(t)
	nodesURL := *u
	nodesURL.Path = "/_nodes/http"

	client := &http.Client{
		Transport: GetTestTransport(t),
		Timeout:   5 * time.Second,
	}

	const (
		maxAttempts     = 30
		pollDelay       = 2 * time.Second
		stableSkipAfter = 3 // skip after this many consecutive identical counts
	)

	var lastTotal, stableCount int
	for attempt := range maxAttempts {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, nodesURL.String(), nil)
		if err != nil {
			t.Fatalf("RequireMinNodes: failed to create request: %v", err)
		}
		if IsSecure(t) {
			req.SetBasicAuth("admin", GetPassword(t))
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Logf("RequireMinNodes: attempt %d/%d: %v", attempt+1, maxAttempts, err)
			time.Sleep(pollDelay)
			continue
		}

		var result struct {
			Nodes struct {
				Total int `json:"total"`
			} `json:"_nodes"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			t.Fatalf("RequireMinNodes: failed to decode /_nodes/http response: %v", err)
		}
		resp.Body.Close()

		total := result.Nodes.Total
		if total >= minNodes {
			if attempt > 0 {
				t.Logf("RequireMinNodes: %d node(s) ready after %d attempts", total, attempt+1)
			}
			return
		}

		// Detect stable (non-growing) cluster and skip early.
		if total == lastTotal {
			stableCount++
		} else {
			stableCount = 1
		}
		lastTotal = total

		if stableCount >= stableSkipAfter {
			t.Skipf("Skipping %s: cluster stable at %d node(s) for %d checks, need at least %d",
				t.Name(), lastTotal, stableCount, minNodes)
		}

		t.Logf("RequireMinNodes: attempt %d/%d: %d node(s), waiting for %d",
			attempt+1, maxAttempts, total, minNodes)
		time.Sleep(pollDelay)
	}

	// Cluster is reachable but doesn't have enough nodes — skip.
	t.Skipf("Skipping %s: cluster has %d node(s) after %d attempts, need at least %d",
		t.Name(), lastTotal, maxAttempts, minNodes)
}

// WaitForCluster waits for the OpenSearch cluster to be reachable and responding to HTTP requests.
// Unlike WaitForClusterReady in opensearchapi/testutil, this uses raw HTTP requests and does not
// require an opensearchapi.Client. This is useful for tests that need to wait for cluster
// availability before creating a transport.
func WaitForCluster(t *testing.T) {
	t.Helper()

	u := GetTestURL(t)
	healthURL := *u
	healthURL.Path = "/"

	httpClient := &http.Client{Transport: GetTestTransport(t)}

	var prepareReq func(*http.Request)
	if IsSecure(t) {
		password := GetPassword(t)
		prepareReq = func(req *http.Request) {
			req.SetBasicAuth("admin", password)
		}
	}

	readiness.Wait(t, t.Context(), readiness.LayerHTTP,
		readiness.WithExpectedNodes(1),
		readiness.WithRawHTTP(&healthURL, httpClient, prepareReq))
}

// ignoredFieldRule pairs a compiled regexp with a version expression.
type ignoredFieldRule struct {
	Pattern *regexp.Regexp
	Version string // operator+version expression, e.g. ">=v1.0.0"
}

// ignoredFieldRules contains patterns for JSON pointer paths that should be
// ignored during JSON round-trip comparison. Each pattern is a regexp matched
// against the full path from a JSON diff "remove" operation.
//
// Version expressions use operator+version syntax: ">=v1.0.0", ">=v2.0.0,<v3.0.0"
var ignoredFieldRules = []ignoredFieldRule{
	// Dynamic IO statistics that change between calls
	{regexp.MustCompile(`/io_stats/`), ">=v1.0.0"},
	{regexp.MustCompile(`/io_time_in_millis$`), ">=v1.0.0"},
	{regexp.MustCompile(`/queue_size$`), ">=v1.0.0"},
	{regexp.MustCompile(`/read_time$`), ">=v1.0.0"},
	{regexp.MustCompile(`/write_time$`), ">=v1.0.0"},

	// Optional script/template metadata
	{regexp.MustCompile(`/options$`), ">=v1.0.0"},
	{regexp.MustCompile(`/metadata/stored_scripts/`), ">=v1.0.0"},

	// Dynamic cluster/node fields
	{regexp.MustCompile(`/target_node$`), ">=v1.0.0"},
	{regexp.MustCompile(`/relocation_id$`), ">=v1.0.0"},

	// Dynamic task-related fields
	{regexp.MustCompile(`/cancellation_time_millis$`), ">=v1.0.0"},

	// Version-specific or environment-dependent fields
	{regexp.MustCompile(`/build_flavor$`), ">=v1.0.0"},
	{regexp.MustCompile(`/build_type$`), ">=v1.0.0"},
	{regexp.MustCompile(`/build_snapshot$`), ">=v1.0.0"},
	{regexp.MustCompile(`/lucene_version$`), ">=v1.0.0"},

	// Fields present in server responses but not yet in the OpenAPI spec
	{regexp.MustCompile(`/max_last_index_request_timestamp$`), ">=v2.0.0"},
	{regexp.MustCompile(`/merges/warmer$`), ">=v2.0.0"},
	{regexp.MustCompile(`/search/query_failed$`), ">=v2.0.0"},
	{regexp.MustCompile(`/startree_query_`), ">=v3.0.0"},

	// Search hit underscore fields not in SearchResult's hits item schema
	{regexp.MustCompile(`/hits/hits/\d+/_`), ">=v1.0.0"},

	// Deprecated _type field in ingest simulate responses (removed in 2.0+)
	{regexp.MustCompile(`/_type$`), ">=v1.0.0"},

	// Index-level settings/index/replication not in spec
	{regexp.MustCompile(`/settings/index/replication$`), ">=v1.0.0"},

	// Segment merge_id (transient during background merges)
	{regexp.MustCompile(`/segments/[^/]+/merge_id$`), ">=v1.0.0"},

	// Shard stores embed node IDs as dynamic object keys
	{regexp.MustCompile(`/stores/\d+/[A-Za-z0-9_-]{20,}$`), ">=v1.0.0"},

	// Transport SSL settings in nodes info response
	{regexp.MustCompile(`/nodes/[^/]+/settings/transport/ssl$`), ">=v1.0.0"},

	// System-generated search pipeline fields
	{regexp.MustCompile(`/search_pipeline/system_generated_`), ">=v2.0.0"},

	// Node-level indices/status_counter not in spec
	{regexp.MustCompile(`/nodes/[^/]+/indices/status_counter$`), ">=v2.0.0"},

	// Mapping property/field attributes not fully modeled in the spec schema.
	// Covers nested properties, multi-fields, and all per-field settings.
	{regexp.MustCompile(`/mappings/properties/`), ">=v1.0.0"},

	// Node-level search_pipelines/processors not in spec
	{regexp.MustCompile(`/nodes/[^/]+/search_pipelines/processors$`), ">=v2.7.0"},

	// Cluster state metadata fields not in spec
	{regexp.MustCompile(`/metadata/search_pipeline$`), ">=v2.0.0"},
}

// ShouldIgnoreField returns true if the given JSON path represents a field
// that is known to be dynamic, optional, or version-specific and should not
// cause test failures when missing from Go client structs.
func ShouldIgnoreField(t *testing.T, path string, serverVersion string) bool {
	t.Helper()
	for _, rule := range ignoredFieldRules {
		if rule.Pattern.MatchString(path) {
			return EvaluateVersionExpression(t, serverVersion, rule.Version)
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

	comparison := compareVersion(serverVersion, normalizedVersion)

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
		cmp := compareVersion(serverVersion, constraintSemver)
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

// preReleaseRank assigns ordering to known pre-release tags. Lower rank
// = earlier in the release cycle. Per OpenSearch convention:
//
//	snapshot < alpha < beta < rc < (release, i.e. no suffix)
//
// Standard semver (golang.org/x/mod/semver) orders alpha < beta < rc <
// release correctly via lexicographic compare on pre-release identifiers,
// but it places "SNAPSHOT" after "rc" because alphabetic ordering puts
// upper-case 'S' beyond lower-case 'r'. Maven/Gradle SNAPSHOT builds
// represent "in-development past the last release", and treating them
// as later than rc over-skips nightly CI builds that contain the fix.
//

var preReleaseRank = map[string]int{
	"snapshot": 0,
	"alpha":    1,
	"beta":     2,
	"rc":       3,
}

// compareVersion orders two normalized "vX.Y.Z[-pre]" strings using the
// pre-release ranking above. Returns -1 if a < b, 0 if equal, +1 if
// a > b.
func compareVersion(a, b string) int {
	aBase, aPre := splitPrerelease(a)
	bBase, bPre := splitPrerelease(b)

	if cmp := semver.Compare(aBase, bBase); cmp != 0 {
		return cmp
	}
	return comparePrerelease(aPre, bPre)
}

// splitPrerelease returns the base "vX.Y.Z" portion and the lower-cased
// pre-release tag (without the leading "-"). Empty pre-release means
// "is a release version".
func splitPrerelease(v string) (string, string) {
	if before, after, ok := strings.Cut(v, "-"); ok {
		return before, strings.ToLower(after)
	}
	return v, ""
}

// comparePrerelease compares two lower-cased pre-release strings. The
// empty string represents "release" (no pre-release suffix) and ranks
// highest. Known tags use preReleaseRank; tied ranks fall through to
// lexicographic compare so e.g. "rc.1" < "rc.2".
func comparePrerelease(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return +1 // release beats any pre-release
	}
	if b == "" {
		return -1
	}

	aRank, aKnown := preReleaseRank[preReleaseHead(a)]
	bRank, bKnown := preReleaseRank[preReleaseHead(b)]

	switch {
	case aKnown && bKnown:
		if aRank != bRank {
			if aRank < bRank {
				return -1
			}
			return +1
		}
	case aKnown:
		return -1 // known tag sorts before unknown
	case bKnown:
		return +1
	}

	if a < b {
		return -1
	}
	return +1
}

// preReleaseHead returns the leading alpha tag of a pre-release string,
// stripping any trailing numeric or punctuation suffix. "rc.1" -> "rc",
// "alpha-2" -> "alpha", "snapshot" -> "snapshot".
func preReleaseHead(s string) string {
	for i, r := range s {
		if r == '.' || r == '-' || (r >= '0' && r <= '9') {
			return s[:i]
		}
	}
	return s
}

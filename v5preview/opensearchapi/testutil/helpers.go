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

	"github.com/stretchr/testify/require"
	"github.com/wI2L/jsondiff"
	"golang.org/x/mod/semver"
	"golang.org/x/sync/semaphore"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/internal/test/readiness"
	tptestutil "github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
	"github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi"
)

// readinessSem limits how many goroutines can run WaitForClusterReady
// concurrently.
//
//nolint:gochecknoglobals // test-only process-level throttle
var readinessSem = semaphore.NewWeighted(2)

// sharedClient is a per-package singleton that avoids redundant cluster
// readiness checks.
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
	MustUniqueString  = tptestutil.MustUniqueString
	IsDebugEnabled    = tptestutil.IsDebugEnabled
	IsSecure          = tptestutil.IsSecure
	GetTestURL        = tptestutil.GetTestURL
	GetPassword       = tptestutil.GetPassword
	GetTestTransport  = tptestutil.GetTestTransport
	SkipIfNotSecure   = tptestutil.SkipIfNotSecure
	RequireMinNodes   = tptestutil.RequireMinNodes
	ShouldIgnoreField = tptestutil.ShouldIgnoreField
	PollUntil         = tptestutil.PollUntil
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
// Supported operators: =, !=, <, <=, >, >=
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

func parseVersion(t *testing.T, version string) (int64, int64, int64, bool) {
	t.Helper()

	var major, minor, patch int64
	var hasPatch bool

	parts := strings.Split(version, ".")
	switch len(parts) {
	case 2:
		var err error
		major, err = strconv.ParseInt(parts[0], 10, 64)
		require.NoError(t, err)
		minor, err = strconv.ParseInt(parts[1], 10, 64)
		require.NoError(t, err)
	case 3:
		hasPatch = true
		var err error
		major, err = strconv.ParseInt(parts[0], 10, 64)
		require.NoError(t, err)
		minor, err = strconv.ParseInt(parts[1], 10, 64)
		require.NoError(t, err)
		patch, err = strconv.ParseInt(parts[2], 10, 64)
		require.NoError(t, err)
	default:
		t.Fatalf("SkipIfVersion: version %q must be major.minor or major.minor.patch", version)
	}

	return major, minor, patch, hasPatch
}

// ExtendedReadinessCheck performs validation checks to ensure the
// cluster is ready for complex operations
func ExtendedReadinessCheck(t *testing.T, ctx context.Context, client *opensearchapi.Client) error {
	t.Helper()

	_, err := client.Cluster.State(ctx, nil)
	if err != nil {
		return fmt.Errorf("cluster state check failed: %w", err)
	}

	_, err = client.Nodes.Info(ctx, nil)
	if err != nil {
		return fmt.Errorf("nodes info check failed: %w", err)
	}

	return nil
}

// NewClient returns a shared opensearchapi.Client that is safe for concurrent
// use across tests within the same package. The client is constructed
// once; each caller re-verifies cluster readiness so partial-startup
// failures from earlier tests don't yield a half-broken shared client.
func NewClient(t *testing.T) (*opensearchapi.Client, error) {
	t.Helper()
	sharedClientOnce.Do(func() {
		config := ClientConfig(t)
		config.Client.Context = context.Background()

		var err error
		sharedClient, err = opensearchapi.NewClient(*config)
		if err != nil {
			errSharedClient = err
			return
		}
	})
	if errSharedClient != nil {
		return nil, fmt.Errorf("shared client construction failed: %w", errSharedClient)
	}
	WaitForClusterReady(t, sharedClient)
	return sharedClient, nil
}

// InitClient creates a new opensearchapi.Client with full cluster readiness checking.
func InitClient(t *testing.T) (*opensearchapi.Client, error) {
	t.Helper()
	config := ClientConfig(t)

	client, err := opensearchapi.NewClient(*config)
	if err != nil {
		return nil, err
	}

	WaitForClusterReady(t, client)
	return client, nil
}

// WaitForClusterReady blocks until the OpenSearch cluster responds to
// GET / using the layered readiness FSM in internal/test/readiness. The
// readinessSem caps concurrent setups across tests so a stampede of
// goroutines doesn't overload a small CI cluster.
func WaitForClusterReady(t *testing.T, client *opensearchapi.Client) {
	t.Helper()
	if err := readinessSem.Acquire(t.Context(), 1); err != nil {
		require.NoError(t, err, "readiness semaphore acquire")
		return
	}
	defer readinessSem.Release(1)
	readiness.Wait(t, t.Context(), readiness.LayerHTTP,
		readiness.WithFSMCheck(clusterLensFSMCheck(client, expectedNodeCount())))
}

// WaitForAllNodesReady blocks until every node in the test cluster has
// reached LayerStatsReady - i.e. cluster-health reports the expected
// node count AND _cat/nodes returns non-nil cpu+heap.percent for each
// node. It uses the layered readiness FSM in internal/test/readiness so
// that timeouts produce a structured per-node diagnostic instead of a
// "Condition never satisfied" stub.
//
// Expected node count comes from OPENSEARCH_NODE_COUNT (defaults to 1).
// Per-layer budgets are tuned for CI pessimism (cold JVM startup is the
// long pole); see readiness.DefaultBudgets for the exact values.
func WaitForAllNodesReady(t *testing.T, client *opensearchapi.Client) {
	t.Helper()
	readiness.Wait(t, t.Context(), readiness.TargetClusterReady,
		readiness.WithFSMCheck(clusterLensFSMCheck(client, expectedNodeCount())))
}

// expectedNodeCount returns the OPENSEARCH_NODE_COUNT env value, defaulting to 1.
func expectedNodeCount() int {
	v := os.Getenv("OPENSEARCH_NODE_COUNT")
	if v == "" {
		return 1
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// CompareRawJSONwithParsedJSON is a helper function to determine the difference between the parsed JSON and the raw JSON.
// This is helpful to detect missing fields in the go structs.
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

	if string(parsedBody) != string(body) {
		patch, err := jsondiff.CompareJSON(body, parsedBody)
		require.NoError(t, err)
		operations := make([]jsondiff.Operation, 0)
		for _, operation := range patch {
			if operation.Type == "add" && operation.Path != "" {
				continue
			}

			if operation.Type == "remove" && isEmptyCollection(operation.OldValue) {
				continue
			}

			if tptestutil.ShouldIgnoreField(t, operation.Path, "") {
				continue
			}

			operations = append(operations, operation)
		}
		if len(operations) == 0 {
			return
		}
		for _, op := range operations {
			t.Logf("%s", op)
		}
		t.Logf("raw body: %s", body)
		require.Empty(t, operations)
	}
}

func isEmptyCollection(v any) bool {
	switch val := v.(type) {
	case []any:
		return len(val) == 0
	case map[string]any:
		return len(val) == 0
	}
	return false
}

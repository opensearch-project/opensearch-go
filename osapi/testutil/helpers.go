// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package testutil provides test helpers that depend on osapi and opensearch types.
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
	tptestutil "github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
	"github.com/opensearch-project/opensearch-go/v4/osapi"
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
	sharedClient     *osapi.Client
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

// ClientConfig returns an osapi.Config for both secure and insecure opensearch
func ClientConfig(t *testing.T) *osapi.Config {
	t.Helper()

	cfg := &osapi.Config{
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
func GetVersion(t *testing.T, ctx context.Context, client *osapi.Client) (int64, int64, int64, error) {
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
func SkipIfVersion(t *testing.T, client *osapi.Client, operator string, version string, testName string) {
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
func ExtendedReadinessCheck(t *testing.T, ctx context.Context, client *osapi.Client) error {
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

// NewClient returns a shared osapi.Client that is safe for concurrent
// use across tests within the same package. The client is constructed
// once; each caller re-verifies cluster readiness so partial-startup
// failures from earlier tests don't yield a half-broken shared client.
func NewClient(t *testing.T) (*osapi.Client, error) {
	t.Helper()
	sharedClientOnce.Do(func() {
		config := ClientConfig(t)
		config.Client.Context = context.Background()

		var err error
		sharedClient, err = osapi.NewClient(*config)
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

// InitClient creates a new osapi.Client with full cluster readiness checking.
func InitClient(t *testing.T) (*osapi.Client, error) {
	t.Helper()
	config := ClientConfig(t)

	client, err := osapi.NewClient(*config)
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
func WaitForClusterReady(t *testing.T, client *osapi.Client) {
// goroutines doesn't overload a small CI cluster.
func WaitForClusterReady(t *testing.T, client *osapi.Client) {
	t.Helper()
	if err := readinessSem.Acquire(t.Context(), 1); err != nil {
		require.NoError(t, err, "readiness semaphore acquire")
		return
	}
	defer readinessSem.Release(1)
	readiness.Wait(t, t.Context(), readiness.LayerHTTP, readiness.WithCluster(client))
}

// WaitForAllNodesReady polls /_cat/nodes until every node reports non-nil cpu
// and heap.percent metrics. This prevents flakes from nodes that haven't fully
// initialized in CI (e.g. stats not yet collected after a fresh cluster start).
func WaitForAllNodesReady(t *testing.T, client *osapi.Client) {
	t.Helper()
	require.Eventually(t, func() bool {
		resp, err := client.Cat.Nodes(t.Context(), &osapi.CatNodesReq{
			Params: &osapi.CatNodesParams{DebugParams: osapi.DebugParams{Format: "json"}},
		})
		if err != nil || resp == nil || len(resp.Records) == 0 {
			return false
		}
		for _, node := range resp.Records {
			if node.Cpu == nil || node.HeapPercent == nil {
				return false
			}
		}
		return true
	}, 60*time.Second, 1*time.Second, "not all nodes reporting stats (cpu/heap.percent nil)")
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

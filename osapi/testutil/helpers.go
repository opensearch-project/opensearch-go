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
	"time"

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
	SkipIfSingleNode  = tptestutil.SkipIfSingleNode
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
// use across tests within the same package. The client is created once with
// full cluster readiness checking; subsequent calls return immediately.
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

		errSharedClient = WaitForClusterReady(t, sharedClient)
	})
	if errSharedClient != nil {
		return nil, fmt.Errorf("shared client initialization failed: %w", errSharedClient)
	}
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

	err = WaitForClusterReady(t, client)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func classifyConnError(err error, everConnected bool, eofCount *int) error {
	errMsg := err.Error()

	if strings.Contains(errMsg, "invalid character 'U'") ||
		strings.Contains(errMsg, "Unauthorized") {
		return fmt.Errorf("cluster returned Unauthorized (SECURE_INTEGRATION=%s, OPENSEARCH_VERSION=%s); "+
			"verify credentials are correct: %w",
			os.Getenv("SECURE_INTEGRATION"), os.Getenv("OPENSEARCH_VERSION"), err)
	}

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
func WaitForClusterReady(t *testing.T, client *osapi.Client) error {
	t.Helper()

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
		eofCount = 0
		everConnected = true

		resp, err := client.Cluster.Health(ctx, nil)
		if err != nil || resp == nil {
			cancel()
			t.Logf("Waiting %s for cluster readiness (attempt %d/%d)...", delayBetweenAttempts, attempt+1, maxAttempts)
			time.Sleep(delayBetweenAttempts)
			continue
		}

		readyErr := ExtendedReadinessCheck(t, ctx, client)
		if readyErr == nil {
			cancel()
			if attempt > 0 {
				t.Logf("Cluster ready after %d attempts (version %d.%d.%d)", attempt+1, major, minor, patch)
			}
			return nil
		}

		t.Logf("Cluster health OK but readiness validation failed (attempt %d/%d): %v", attempt+1, maxAttempts, readyErr)
		cancel()
		time.Sleep(delayBetweenAttempts)
	}

	return fmt.Errorf("cluster not ready after %d attempts (version %d.%d.%d)", maxAttempts, major, minor, patch)
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

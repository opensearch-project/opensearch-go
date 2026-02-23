// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// validOpenSearchRootResponse returns a minimal valid GET / response body.
func validOpenSearchRootResponse() string {
	return `{
  "name": "test-node",
  "cluster_name": "test-cluster",
  "cluster_uuid": "test-uuid",
  "version": {
    "number": "3.4.0",
    "build_type": "tar",
    "build_hash": "abc123",
    "build_date": "2024-01-01T00:00:00.000Z",
    "build_snapshot": false,
    "lucene_version": "9.11.0",
    "minimum_wire_compatibility_version": "7.10.0",
    "minimum_index_compatibility_version": "7.0.0",
    "distribution": "opensearch"
  },
  "tagline": "The OpenSearch Project: https://opensearch.org/"
}`
}

// validClusterHealthResponse returns a minimal valid /_cluster/health response body.
func validClusterHealthResponse() string {
	return `{
  "cluster_name": "test-cluster",
  "status": "green",
  "timed_out": false,
  "number_of_nodes": 3,
  "number_of_data_nodes": 3,
  "active_primary_shards": 5,
  "active_shards": 10,
  "relocating_shards": 0,
  "initializing_shards": 0,
  "unassigned_shards": 0,
  "delayed_unassigned_shards": 0,
  "number_of_pending_tasks": 0,
  "number_of_in_flight_fetch": 0,
  "task_max_waiting_in_queue_millis": 0,
  "active_shards_percent_as_number": 100.0
}`
}

// newTestClient creates a minimal Client suitable for health check testing with the given transport.
// Uses small timeouts and zero jitter for fast, deterministic tests.
func newTestClient(transport http.RoundTripper) *Client {
	return &Client{
		transport:             transport,
		healthCheckTimeout:    1 * time.Second,
		healthCheckJitter:     0.0,
		maxRetryClusterHealth: 50 * time.Millisecond,
	}
}

func TestClusterHealthStateFlags(t *testing.T) {
	t.Run("zero value is pending", func(t *testing.T) {
		var e clusterHealthProbeState
		require.True(t, e.Pending())
		require.False(t, e.HasClusterHealth())
		require.False(t, e.Unavailable())
	})

	t.Run("checked only is unavailable", func(t *testing.T) {
		e := clusterHealthProbed
		require.False(t, e.Pending())
		require.False(t, e.HasClusterHealth())
		require.True(t, e.Unavailable())
	})

	t.Run("checked and available is HasClusterHealth", func(t *testing.T) {
		e := clusterHealthProbed | clusterHealthAvailable
		require.False(t, e.Pending())
		require.True(t, e.HasClusterHealth())
		require.False(t, e.Unavailable())
	})

	t.Run("available without checked is not HasClusterHealth", func(t *testing.T) {
		// This is an invalid state but the methods should handle it safely
		e := clusterHealthAvailable
		require.False(t, e.Pending())
		require.False(t, e.HasClusterHealth())
		require.False(t, e.Unavailable())
	})

	t.Run("loadClusterHealthState returns correct value", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{}}
		require.True(t, conn.loadClusterHealthState().Pending())

		conn.clusterHealthState.Store(int64(clusterHealthProbed | clusterHealthAvailable))
		require.True(t, conn.loadClusterHealthState().HasClusterHealth())

		conn.clusterHealthState.Store(int64(clusterHealthProbed))
		require.True(t, conn.loadClusterHealthState().Unavailable())

		conn.clusterHealthState.Store(0)
		require.True(t, conn.loadClusterHealthState().Pending())
	})
}

func TestClusterHealthLocal_Parsing(t *testing.T) {
	t.Run("parses valid response", func(t *testing.T) {
		var health ClusterHealthLocal
		err := json.Unmarshal([]byte(validClusterHealthResponse()), &health)
		require.NoError(t, err)

		require.Equal(t, "test-cluster", health.ClusterName)
		require.Equal(t, "green", health.Status)
		require.False(t, health.TimedOut)
		require.Equal(t, 3, health.NumberOfNodes)
		require.Equal(t, 3, health.NumberOfDataNodes)
		require.Equal(t, 5, health.ActivePrimaryShards)
		require.Equal(t, 10, health.ActiveShards)
		require.Equal(t, 0, health.UnassignedShards)
		require.InDelta(t, 100.0, health.ActiveShardsPercentAsNumber, 0.001)
		require.Nil(t, health.DiscoveredClusterManager)
	})

	t.Run("parses discovered_cluster_manager field", func(t *testing.T) {
		body := `{
			"cluster_name": "test",
			"status": "yellow",
			"timed_out": false,
			"number_of_nodes": 1,
			"number_of_data_nodes": 1,
			"active_primary_shards": 1,
			"active_shards": 1,
			"relocating_shards": 0,
			"initializing_shards": 0,
			"unassigned_shards": 1,
			"delayed_unassigned_shards": 0,
			"number_of_pending_tasks": 0,
			"number_of_in_flight_fetch": 0,
			"task_max_waiting_in_queue_millis": 0,
			"active_shards_percent_as_number": 50.0,
			"discovered_cluster_manager": true
		}`

		var health ClusterHealthLocal
		err := json.Unmarshal([]byte(body), &health)
		require.NoError(t, err)

		require.NotNil(t, health.DiscoveredClusterManager)
		require.True(t, *health.DiscoveredClusterManager)
	})

	t.Run("round-trips through JSON", func(t *testing.T) {
		original := ClusterHealthLocal{
			ClusterName:                 "roundtrip-cluster",
			Status:                      "yellow",
			TimedOut:                    false,
			NumberOfNodes:               5,
			NumberOfDataNodes:           3,
			ActivePrimaryShards:         10,
			ActiveShards:                20,
			ActiveShardsPercentAsNumber: 95.5,
		}

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var parsed ClusterHealthLocal
		err = json.Unmarshal(data, &parsed)
		require.NoError(t, err)

		require.Equal(t, original.ClusterName, parsed.ClusterName)
		require.Equal(t, original.Status, parsed.Status)
		require.Equal(t, original.NumberOfNodes, parsed.NumberOfNodes)
		require.InDelta(t, original.ActiveShardsPercentAsNumber, parsed.ActiveShardsPercentAsNumber, 0.001)
	})
}

func TestProbeClusterHealth_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_cluster/health":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(validClusterHealthResponse()))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	conn := &Connection{URL: serverURL}
	client := newTestClient(server.Client().Transport)

	require.True(t, conn.loadClusterHealthState().Pending())

	client.probeClusterHealthLocal(t.Context(), conn, serverURL, nil)

	info := conn.loadClusterHealthState()
	require.True(t, info.HasClusterHealth(), "should be marked as available after successful probe")
	require.False(t, info.Pending())
	require.False(t, info.Unavailable())

	conn.mu.RLock()
	health := conn.mu.clusterHealth
	checkedAt := conn.mu.clusterHealthCheckedAt
	conn.mu.RUnlock()

	require.NotNil(t, health, "clusterHealth should be populated")
	require.Equal(t, "test-cluster", health.ClusterName)
	require.Equal(t, "green", health.Status)
	require.Equal(t, 3, health.NumberOfNodes)
	require.False(t, checkedAt.IsZero(), "clusterHealthCheckedAt should be set")
}

func TestProbeClusterHealth_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_cluster/health":
			// Simulate missing cluster:monitor/health privilege
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"no permissions for [cluster:monitor/health]"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	conn := &Connection{URL: serverURL}
	client := newTestClient(server.Client().Transport)

	require.True(t, conn.loadClusterHealthState().Pending())

	client.probeClusterHealthLocal(t.Context(), conn, serverURL, nil)

	info := conn.loadClusterHealthState()
	require.True(t, info.Unavailable(), "should be marked as unavailable after 403")
	require.False(t, info.Pending())
	require.False(t, info.HasClusterHealth())

	conn.mu.RLock()
	health := conn.mu.clusterHealth
	checkedAt := conn.mu.clusterHealthCheckedAt
	conn.mu.RUnlock()

	require.Nil(t, health, "clusterHealth should remain nil on 403")
	require.False(t, checkedAt.IsZero(), "clusterHealthCheckedAt should be set for retry timing")
}

func TestProbeClusterHealth_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"authentication required"}`))
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	conn := &Connection{URL: serverURL}
	client := newTestClient(server.Client().Transport)

	client.probeClusterHealthLocal(t.Context(), conn, serverURL, nil)

	info := conn.loadClusterHealthState()
	require.True(t, info.Unavailable(), "should be marked as unavailable after 401")
}

func TestProbeClusterHealth_TransientError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	conn := &Connection{URL: serverURL}
	client := newTestClient(server.Client().Transport)

	require.True(t, conn.loadClusterHealthState().Pending())

	client.probeClusterHealthLocal(t.Context(), conn, serverURL, nil)

	// Transient errors should leave state at pending (0) -- retried on next health check
	info := conn.loadClusterHealthState()
	require.True(t, info.Pending(), "should remain pending after transient 500 error")

	conn.mu.RLock()
	checkedAt := conn.mu.clusterHealthCheckedAt
	conn.mu.RUnlock()

	require.True(t, checkedAt.IsZero(), "clusterHealthCheckedAt should NOT be set on transient error")
}

func TestDefaultHealthCheck_PromotesToClusterHealth(t *testing.T) {
	// Track which endpoints are called
	var rootCalls, healthCalls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			rootCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(validOpenSearchRootResponse()))
		case "/_cluster/health":
			healthCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(validClusterHealthResponse()))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	conn := &Connection{URL: serverURL}
	client := newTestClient(server.Client().Transport)

	ctx := context.Background()

	// First call: conn is pending -> uses baseline GET /, launches async probe
	resp, err := client.DefaultHealthCheck(ctx, conn, serverURL)
	require.NoError(t, err)
	require.NotNil(t, resp)
	if resp.Body != nil {
		resp.Body.Close()
	}

	require.Equal(t, int64(1), rootCalls.Load(), "first call should hit GET /")

	// Wait for the async probe to complete
	require.Eventually(t, func() bool {
		return conn.loadClusterHealthState().HasClusterHealth()
	}, 2*time.Second, 10*time.Millisecond, "probe should mark connection as having cluster health")

	require.Equal(t, int64(1), healthCalls.Load(), "probe should call /_cluster/health once")

	// Second call: conn has cluster health -> should use /_cluster/health directly
	rootCallsBefore := rootCalls.Load()
	healthCallsBefore := healthCalls.Load()

	resp, err = client.DefaultHealthCheck(ctx, conn, serverURL)
	require.NoError(t, err)
	require.NotNil(t, resp)
	if resp.Body != nil {
		resp.Body.Close()
	}

	require.Equal(t, rootCallsBefore, rootCalls.Load(), "second call should NOT hit GET /")
	require.Equal(t, healthCallsBefore+1, healthCalls.Load(), "second call should hit /_cluster/health")
}

func TestDefaultHealthCheck_FallbackOnPermissionRevoked(t *testing.T) {
	// Start with cluster health available, then simulate permission revocation
	var returnForbidden atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(validOpenSearchRootResponse()))
		case "/_cluster/health":
			if returnForbidden.Load() {
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"error":"no permissions for [cluster:monitor/health]"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(validClusterHealthResponse()))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	conn := &Connection{URL: serverURL}
	client := newTestClient(server.Client().Transport)

	ctx := context.Background()

	// Set up connection as having cluster health available
	conn.clusterHealthState.Store(int64(clusterHealthProbed | clusterHealthAvailable))
	conn.mu.Lock()
	conn.mu.clusterHealth = &ClusterHealthLocal{
		ClusterName:   "test-cluster",
		Status:        "green",
		NumberOfNodes: 3,
	}
	conn.mu.clusterHealthCheckedAt = time.Now()
	conn.mu.Unlock()

	// Verify cluster health check works initially
	resp, err := client.DefaultHealthCheck(ctx, conn, serverURL)
	require.NoError(t, err)
	require.NotNil(t, resp)
	if resp.Body != nil {
		resp.Body.Close()
	}
	require.True(t, conn.loadClusterHealthState().HasClusterHealth())

	// Revoke permission
	returnForbidden.Store(true)

	// Next health check should fall back to GET / and reset state
	resp, err = client.DefaultHealthCheck(ctx, conn, serverURL)
	require.NoError(t, err, "should fall back to GET / and succeed")
	require.NotNil(t, resp)
	if resp.Body != nil {
		resp.Body.Close()
	}

	// Verify state was reset
	info := conn.loadClusterHealthState()
	require.True(t, info.Pending(), "cluster health should be reset to pending after 403")

	conn.mu.RLock()
	health := conn.mu.clusterHealth
	checkedAt := conn.mu.clusterHealthCheckedAt
	conn.mu.RUnlock()

	require.Nil(t, health, "stale cluster health data should be zeroed out")
	require.True(t, checkedAt.IsZero(), "clusterHealthCheckedAt should be cleared")
}

func TestDefaultHealthCheck_RetryAfterMaxRetry(t *testing.T) {
	var healthCalls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(validOpenSearchRootResponse()))
		case "/_cluster/health":
			healthCalls.Add(1)
			// Always return 403 for this test
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"no permissions"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	conn := &Connection{URL: serverURL}
	client := newTestClient(server.Client().Transport)

	ctx := context.Background()

	// Mark connection as unavailable with a recent timestamp
	conn.clusterHealthState.Store(int64(clusterHealthProbed))
	conn.mu.Lock()
	conn.mu.clusterHealthCheckedAt = time.Now()
	conn.mu.Unlock()

	// Call DefaultHealthCheck -- retry interval hasn't elapsed, should NOT re-probe
	resp, err := client.DefaultHealthCheck(ctx, conn, serverURL)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}

	// Give async goroutines time to run (if any were launched erroneously)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int64(0), healthCalls.Load(), "should NOT re-probe when interval hasn't elapsed")

	// Now set the timestamp far enough in the past
	conn.mu.Lock()
	conn.mu.clusterHealthCheckedAt = time.Now().Add(-100 * time.Millisecond)
	conn.mu.Unlock()

	// Call DefaultHealthCheck -- retry interval has elapsed, should re-probe
	resp, err = client.DefaultHealthCheck(ctx, conn, serverURL)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}

	// Wait for async probe to run
	require.Eventually(t, func() bool {
		return healthCalls.Load() > 0
	}, 2*time.Second, 10*time.Millisecond, "should re-probe after interval elapses")

	// The probe will get a 403 again, so it should still be unavailable
	require.Eventually(t, func() bool {
		return conn.loadClusterHealthState().Unavailable()
	}, 2*time.Second, 10*time.Millisecond, "should remain unavailable after 403 on retry")
}

func TestDefaultHealthCheck_NilConnection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(validOpenSearchRootResponse()))
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	client := newTestClient(server.Client().Transport)

	ctx := context.Background()

	// Should fall through to baseline without panicking
	resp, err := client.DefaultHealthCheck(ctx, nil, serverURL)
	require.NoError(t, err)
	require.NotNil(t, resp)
	if resp.Body != nil {
		resp.Body.Close()
	}
}

func TestDefaultHealthCheck_DisabledClusterHealthProbing(t *testing.T) {
	var healthCalls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(validOpenSearchRootResponse()))
		case "/_cluster/health":
			healthCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(validClusterHealthResponse()))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	conn := &Connection{URL: serverURL}
	client := newTestClient(server.Client().Transport)
	// maxRetryClusterHealth was set to 0 by New() when Config value is <0
	// (disable probing entirely means the resolved value is 0)
	client.maxRetryClusterHealth = 0

	ctx := context.Background()

	// Even though conn is pending, probing is disabled -- should not launch probe
	resp, err := client.DefaultHealthCheck(ctx, conn, serverURL)
	require.NoError(t, err)
	require.NotNil(t, resp)
	if resp.Body != nil {
		resp.Body.Close()
	}

	// Give async goroutines time to run (if any were launched erroneously)
	time.Sleep(100 * time.Millisecond)

	// With maxRetryClusterHealth=0, the Pending() case still launches a probe.
	// The "disable probing entirely" behavior is controlled by Config value <0 -> resolved to 0,
	// but the Pending() case always probes once. Verify the unavailable retry case is blocked.
	conn.clusterHealthState.Store(int64(clusterHealthProbed))
	conn.mu.Lock()
	conn.mu.clusterHealthCheckedAt = time.Now().Add(-24 * time.Hour) // Long ago
	conn.mu.Unlock()

	healthCalls.Store(0) // Reset counter

	resp, err = client.DefaultHealthCheck(ctx, conn, serverURL)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}

	time.Sleep(100 * time.Millisecond)
	require.Equal(t, int64(0), healthCalls.Load(),
		"should NOT retry probe when maxRetryClusterHealth is 0 (disabled)")
}

func TestHealthCheckRequestModifier(t *testing.T) {
	// Channels to signal when specific requests arrive
	rootRequestCh := make(chan http.Header, 1)
	healthRequestCh := make(chan http.Header, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Clone()

		switch r.URL.Path {
		case "/":
			select {
			case rootRequestCh <- header:
			default:
				// Already sent one, ignore duplicates
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(validOpenSearchRootResponse()))
		case "/_cluster/health":
			select {
			case healthRequestCh <- header:
			default:
				// Already sent one, ignore duplicates
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(validClusterHealthResponse()))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	conn := &Connection{URL: serverURL}
	client := newTestClient(server.Client().Transport)
	client.healthCheckRequestModifier = func(req *http.Request) {
		req.Header.Set("X-Custom-Auth", "bearer-token-123")
	}

	ctx := context.Background()

	// Baseline health check -- modifier should be applied
	resp, err := client.DefaultHealthCheck(ctx, conn, serverURL)
	require.NoError(t, err)
	require.NotNil(t, resp)
	if resp.Body != nil {
		resp.Body.Close()
	}

	// Verify the root request had the custom header
	select {
	case header := <-rootRequestCh:
		require.Equal(t, "bearer-token-123", header.Get("X-Custom-Auth"),
			"modifier should apply custom header to baseline health check")
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for root request")
	}

	// Wait for async probe to complete and verify its header
	select {
	case header := <-healthRequestCh:
		require.Equal(t, "bearer-token-123", header.Get("X-Custom-Auth"),
			"modifier should apply to /_cluster/health probe requests")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for async probe request")
	}

	// Wait for cluster health info to be loaded
	require.Eventually(t, func() bool {
		return conn.loadClusterHealthState().HasClusterHealth()
	}, 2*time.Second, 10*time.Millisecond)

	// Reset channels for next test
	select {
	case <-rootRequestCh:
	default:
	}
	select {
	case <-healthRequestCh:
	default:
	}

	// Now test that subsequent health checks also use the modifier
	resp, err = client.DefaultHealthCheck(ctx, conn, serverURL)
	require.NoError(t, err)
	require.NotNil(t, resp)
	if resp.Body != nil {
		resp.Body.Close()
	}

	// Should hit the health endpoint since cluster health is now available
	select {
	case header := <-healthRequestCh:
		require.Equal(t, "bearer-token-123", header.Get("X-Custom-Auth"),
			"modifier should apply to cluster health check requests")
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for cluster health check request")
	}
}

// newTestClientWithPool creates a Client with a connection pool set for testing
// periodic refresh methods (pollClusterHealth, snapshotClusterHealthConnections, etc.).
// Uses small timeouts and zero jitter for fast, deterministic tests.
func newTestClientWithPool(transport http.RoundTripper, pool ConnectionPool) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	t := &Client{
		transport:             transport,
		healthCheckTimeout:    1 * time.Second,
		healthCheckJitter:     0.0,
		maxRetryClusterHealth: 50 * time.Millisecond,
		healthCheckRate:       float64(defaultServerCoreCount) * healthCheckRateMultiplier,
		clientsPerServer:      float64(defaultServerCoreCount),
		ctx:                   ctx,
		cancelFunc:            cancel,
	}
	t.mu.connectionPool = pool
	return t
}

func TestCalculateClusterHealthRefreshInterval(t *testing.T) {
	makeClient := func(liveConns int, clientsPerServer, pollRate float64) *Client {
		conns := make([]*Connection, liveConns)
		for i := range conns {
			conns[i] = &Connection{URL: &url.URL{Host: fmt.Sprintf("node%d:9200", i)}}
		}
		pool := &statusConnectionPool{}
		pool.mu.ready = conns
		pool.mu.activeCount = len(conns)
		pool.mu.dead = []*Connection{}

		c := newTestClientWithPool(http.DefaultTransport, pool)
		c.clientsPerServer = clientsPerServer
		c.healthCheckRate = pollRate
		return c
	}

	t.Run("3 nodes default settings", func(t *testing.T) {
		c := makeClient(3, 30.0, 5.0)
		defer c.cancelFunc()
		// 3 * 30 / 5 = 18s
		interval := c.calculateClusterHealthRefreshInterval()
		require.Equal(t, 18*time.Second, interval)
	})

	t.Run("10 nodes default settings", func(t *testing.T) {
		c := makeClient(10, 30.0, 5.0)
		defer c.cancelFunc()
		// 10 * 30 / 5 = 60s
		interval := c.calculateClusterHealthRefreshInterval()
		require.Equal(t, 60*time.Second, interval)
	})

	t.Run("30 nodes default settings", func(t *testing.T) {
		c := makeClient(30, 30.0, 5.0)
		defer c.cancelFunc()
		// 30 * 30 / 5 = 180s = 3min
		interval := c.calculateClusterHealthRefreshInterval()
		require.Equal(t, 3*time.Minute, interval)
	})

	t.Run("clamped to 5s minimum", func(t *testing.T) {
		// 1 * 10 / 5 = 2s -> clamped to 5s
		c := makeClient(1, 10.0, 5.0)
		defer c.cancelFunc()
		interval := c.calculateClusterHealthRefreshInterval()
		require.Equal(t, defaultClusterHealthRefreshMin, interval)
	})

	t.Run("clamped to 5min maximum", func(t *testing.T) {
		// 100 * 30 / 5 = 600s -> clamped to 300s (5min)
		c := makeClient(100, 30.0, 5.0)
		defer c.cancelFunc()
		interval := c.calculateClusterHealthRefreshInterval()
		require.Equal(t, defaultClusterHealthRefreshMax, interval)
	})

	t.Run("zero ready nodes floors to 1", func(t *testing.T) {
		c := makeClient(0, 30.0, 5.0)
		defer c.cancelFunc()
		// floor(0->1): 1 * 30 / 5 = 6s
		interval := c.calculateClusterHealthRefreshInterval()
		require.Equal(t, 6*time.Second, interval)
	})

	t.Run("custom poll rate", func(t *testing.T) {
		// 3 * 30 / 10 = 9s
		c := makeClient(3, 30.0, 10.0)
		defer c.cancelFunc()
		interval := c.calculateClusterHealthRefreshInterval()
		require.Equal(t, 9*time.Second, interval)
	})
}

func TestPollClusterHealth_SingleNodeSkip(t *testing.T) {
	var calls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	conn := &Connection{URL: serverURL}
	conn.clusterHealthState.Store(int64(clusterHealthProbed | clusterHealthAvailable))

	pool := &singleConnectionPool{connection: conn}
	client := newTestClientWithPool(server.Client().Transport, pool)
	defer client.cancelFunc()

	client.pollClusterHealth()

	require.Equal(t, int64(0), calls.Load(), "should not make any HTTP calls for single-node pool")
}

func TestPollClusterHealth_EffectiveSingleNodeSkip(t *testing.T) {
	var calls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	conn := &Connection{URL: serverURL}
	conn.clusterHealthState.Store(int64(clusterHealthProbed | clusterHealthAvailable))

	pool := &statusConnectionPool{}
	pool.mu.ready = []*Connection{conn}
	pool.mu.activeCount = len(pool.mu.ready)
	pool.mu.dead = []*Connection{}

	client := newTestClientWithPool(server.Client().Transport, pool)
	defer client.cancelFunc()

	client.pollClusterHealth()

	require.Equal(t, int64(0), calls.Load(),
		"should not make any HTTP calls for status pool with only 1 total node")
}

func TestSnapshotClusterHealthConnections(t *testing.T) {
	connWithInfo := &Connection{URL: &url.URL{Host: "node1:9200"}}
	connWithInfo.clusterHealthState.Store(int64(clusterHealthProbed | clusterHealthAvailable))

	connPending := &Connection{URL: &url.URL{Host: "node2:9200"}}
	// clusterHealthState defaults to 0 (pending)

	connUnavailable := &Connection{URL: &url.URL{Host: "node3:9200"}}
	connUnavailable.clusterHealthState.Store(int64(clusterHealthProbed))

	connWithInfo2 := &Connection{URL: &url.URL{Host: "node4:9200"}}
	connWithInfo2.clusterHealthState.Store(int64(clusterHealthProbed | clusterHealthAvailable))

	pool := &statusConnectionPool{}
	pool.mu.ready = []*Connection{connWithInfo, connPending, connUnavailable, connWithInfo2}
	pool.mu.activeCount = len(pool.mu.ready)
	pool.mu.dead = []*Connection{}

	client := newTestClientWithPool(http.DefaultTransport, pool)
	defer client.cancelFunc()

	snapshot := client.snapshotClusterHealthConnections()

	require.Len(t, snapshot, 2, "should only include connections with HasClusterHealth()")
	require.Contains(t, snapshot, connWithInfo)
	require.Contains(t, snapshot, connWithInfo2)
	require.NotContains(t, snapshot, connPending)
	require.NotContains(t, snapshot, connUnavailable)
}

func TestRefreshClusterHealth_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_cluster/health" {
			t.Errorf("Expected path /_cluster/health, got %s", r.URL.Path)
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		if r.URL.RawQuery != "local=true" {
			t.Errorf("Expected query local=true, got %s", r.URL.RawQuery)
			http.Error(w, "wrong query", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"cluster_name": "refreshed-cluster",
			"status": "yellow",
			"timed_out": false,
			"number_of_nodes": 5,
			"number_of_data_nodes": 4,
			"active_primary_shards": 20,
			"active_shards": 40,
			"relocating_shards": 1,
			"initializing_shards": 0,
			"unassigned_shards": 2,
			"delayed_unassigned_shards": 0,
			"number_of_pending_tasks": 0,
			"number_of_in_flight_fetch": 0,
			"task_max_waiting_in_queue_millis": 0,
			"active_shards_percent_as_number": 95.2
		}`))
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	conn := &Connection{URL: serverURL}
	conn.clusterHealthState.Store(int64(clusterHealthProbed | clusterHealthAvailable))

	// Pre-populate with old data to verify it gets replaced
	conn.mu.Lock()
	conn.mu.clusterHealth = &ClusterHealthLocal{Status: "green", NumberOfNodes: 3}
	conn.mu.clusterHealthCheckedAt = time.Now().Add(-10 * time.Minute)
	conn.mu.Unlock()

	pool := &statusConnectionPool{}
	pool.mu.ready = []*Connection{conn, {URL: &url.URL{Host: "node2:9200"}}}
	pool.mu.activeCount = len(pool.mu.ready)
	pool.mu.dead = []*Connection{}

	client := newTestClientWithPool(server.Client().Transport, pool)
	defer client.cancelFunc()

	client.refreshClusterHealth(conn)

	// Verify health data was updated
	conn.mu.RLock()
	health := conn.mu.clusterHealth
	checkedAt := conn.mu.clusterHealthCheckedAt
	conn.mu.RUnlock()

	require.NotNil(t, health)
	require.Equal(t, "refreshed-cluster", health.ClusterName)
	require.Equal(t, "yellow", health.Status)
	require.Equal(t, 5, health.NumberOfNodes)
	require.Equal(t, 4, health.NumberOfDataNodes)
	require.Equal(t, 1, health.RelocatingShards)
	require.InDelta(t, 95.2, health.ActiveShardsPercentAsNumber, 0.01)

	// Timestamp should be recent
	require.WithinDuration(t, time.Now(), checkedAt, 2*time.Second)

	// Cluster health state should remain available
	require.True(t, conn.loadClusterHealthState().HasClusterHealth())
}

func TestRefreshClusterHealth_PermissionRevoked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"no permissions for [cluster:monitor/health]"}`))
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	conn := &Connection{URL: serverURL}
	conn.clusterHealthState.Store(int64(clusterHealthProbed | clusterHealthAvailable))

	conn.mu.Lock()
	conn.mu.clusterHealth = &ClusterHealthLocal{Status: "green", NumberOfNodes: 3}
	conn.mu.clusterHealthCheckedAt = time.Now()
	conn.mu.Unlock()

	pool := &statusConnectionPool{}
	pool.mu.ready = []*Connection{conn, {URL: &url.URL{Host: "node2:9200"}}}
	pool.mu.activeCount = len(pool.mu.ready)
	pool.mu.dead = []*Connection{}

	client := newTestClientWithPool(server.Client().Transport, pool)
	defer client.cancelFunc()

	client.refreshClusterHealth(conn)

	// Cluster health should be reset to pending
	require.True(t, conn.loadClusterHealthState().Pending(),
		"should reset to pending after 403")

	// Cluster health should be zeroed out
	conn.mu.RLock()
	health := conn.mu.clusterHealth
	checkedAt := conn.mu.clusterHealthCheckedAt
	conn.mu.RUnlock()

	require.Nil(t, health, "stale cluster health data should be cleared")
	require.True(t, checkedAt.IsZero(), "clusterHealthCheckedAt should be cleared")
}

func TestRefreshClusterHealth_TransientError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"node not ready"}`))
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	conn := &Connection{URL: serverURL}
	conn.clusterHealthState.Store(int64(clusterHealthProbed | clusterHealthAvailable))

	originalHealth := &ClusterHealthLocal{Status: "green", NumberOfNodes: 3}
	originalCheckedAt := time.Now().Add(-5 * time.Minute)

	conn.mu.Lock()
	conn.mu.clusterHealth = originalHealth
	conn.mu.clusterHealthCheckedAt = originalCheckedAt
	conn.mu.Unlock()

	pool := &statusConnectionPool{}
	pool.mu.ready = []*Connection{conn, {URL: &url.URL{Host: "node2:9200"}}}
	pool.mu.activeCount = len(pool.mu.ready)
	pool.mu.dead = []*Connection{}

	client := newTestClientWithPool(server.Client().Transport, pool)
	defer client.cancelFunc()

	client.refreshClusterHealth(conn)

	// Cluster health state should remain available (transient errors don't change state)
	require.True(t, conn.loadClusterHealthState().HasClusterHealth(),
		"state should remain available after transient 503")

	// Cluster health should be preserved (not zeroed)
	conn.mu.RLock()
	health := conn.mu.clusterHealth
	checkedAt := conn.mu.clusterHealthCheckedAt
	conn.mu.RUnlock()

	require.NotNil(t, health, "cluster health should be preserved on transient error")
	require.Equal(t, "green", health.Status)
	require.Equal(t, originalCheckedAt, checkedAt,
		"clusterHealthCheckedAt should not be modified on transient error")
}

func TestClusterHealthCheck_TransientFallback(t *testing.T) {
	var rootCalls, healthCalls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			rootCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(validOpenSearchRootResponse()))
		case "/_cluster/health":
			healthCalls.Add(1)
			// Return 503 Service Unavailable (transient error)
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":"node not ready"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	serverURL, _ := url.Parse(server.URL)
	conn := &Connection{URL: serverURL}
	client := newTestClient(server.Client().Transport)

	// Pre-set as having cluster health
	conn.clusterHealthState.Store(int64(clusterHealthProbed | clusterHealthAvailable))
	conn.mu.Lock()
	conn.mu.clusterHealth = &ClusterHealthLocal{Status: "green", NumberOfNodes: 3}
	conn.mu.Unlock()

	ctx := context.Background()

	// clusterHealthCheck should get 503, fall back to baseline GET /
	resp, err := client.DefaultHealthCheck(ctx, conn, serverURL)
	require.NoError(t, err, "should fall back to GET / and succeed on transient error")
	require.NotNil(t, resp)
	if resp.Body != nil {
		resp.Body.Close()
	}

	require.Equal(t, int64(1), healthCalls.Load(), "should have tried /_cluster/health")
	require.Equal(t, int64(1), rootCalls.Load(), "should have fallen back to GET /")

	// Cluster health state should NOT be changed on transient errors
	info := conn.loadClusterHealthState()
	require.True(t, info.HasClusterHealth(), "state should remain available after transient error")

	conn.mu.RLock()
	health := conn.mu.clusterHealth
	conn.mu.RUnlock()
	require.NotNil(t, health, "cluster health data should be preserved on transient error")
}

func TestEvaluateOverload(t *testing.T) {
	makeClient := func() *Client {
		return &Client{
			overloadedHeapThreshold: defaultOverloadedHeapThreshold,
			overloadedBreakerRatio:  defaultOverloadedBreakerRatio,
		}
	}

	makeConn := func() *Connection {
		return &Connection{URL: &url.URL{Host: "node1:9200"}}
	}

	t.Run("no overload with healthy stats", func(t *testing.T) {
		c := makeClient()
		conn := makeConn()
		stats := &NodeStats{
			JVM: JVMStats{Mem: JVMMemStats{HeapUsedPercent: 50}},
			Breakers: map[string]BreakerStats{
				"fielddata": {LimitSizeInBytes: 1000, EstimatedSizeInBytes: 100, Tripped: 0},
			},
		}

		require.False(t, c.evaluateOverload(conn, stats))
	})

	t.Run("heap overload", func(t *testing.T) {
		c := makeClient()
		conn := makeConn()
		stats := &NodeStats{
			JVM:      JVMStats{Mem: JVMMemStats{HeapUsedPercent: 90}},
			Breakers: map[string]BreakerStats{},
		}

		require.True(t, c.evaluateOverload(conn, stats))
	})

	t.Run("breaker ratio overload", func(t *testing.T) {
		c := makeClient()
		conn := makeConn()
		stats := &NodeStats{
			JVM: JVMStats{Mem: JVMMemStats{HeapUsedPercent: 50}},
			Breakers: map[string]BreakerStats{
				"parent": {LimitSizeInBytes: 1000, EstimatedSizeInBytes: 950, Tripped: 0},
			},
		}

		require.True(t, c.evaluateOverload(conn, stats))
	})

	t.Run("breaker trip delta", func(t *testing.T) {
		c := makeClient()
		conn := makeConn()
		stats := &NodeStats{
			JVM: JVMStats{Mem: JVMMemStats{HeapUsedPercent: 50}},
			Breakers: map[string]BreakerStats{
				"fielddata": {LimitSizeInBytes: 1000, EstimatedSizeInBytes: 100, Tripped: 5},
			},
		}

		// First poll: establishes baseline (no delta yet)
		result := c.evaluateOverload(conn, stats)
		require.False(t, result, "first poll should not detect trip delta")

		// Second poll: same trip count, no delta
		result = c.evaluateOverload(conn, stats)
		require.False(t, result, "no new trips should not trigger overload")

		// Third poll: trip count increased
		stats.Breakers["fielddata"] = BreakerStats{LimitSizeInBytes: 1000, EstimatedSizeInBytes: 100, Tripped: 7}
		result = c.evaluateOverload(conn, stats)
		require.True(t, result, "trip count increase should trigger overload")
	})

	t.Run("cluster red status", func(t *testing.T) {
		c := makeClient()
		conn := makeConn()
		conn.mu.Lock()
		conn.mu.clusterHealth = &ClusterHealthLocal{Status: "red"}
		conn.mu.Unlock()

		stats := &NodeStats{
			JVM:      JVMStats{Mem: JVMMemStats{HeapUsedPercent: 50}},
			Breakers: map[string]BreakerStats{},
		}

		require.True(t, c.evaluateOverload(conn, stats))
	})

	t.Run("cluster green status not overloaded", func(t *testing.T) {
		c := makeClient()
		conn := makeConn()
		conn.mu.Lock()
		conn.mu.clusterHealth = &ClusterHealthLocal{Status: "green"}
		conn.mu.Unlock()

		stats := &NodeStats{
			JVM:      JVMStats{Mem: JVMMemStats{HeapUsedPercent: 50}},
			Breakers: map[string]BreakerStats{},
		}

		require.False(t, c.evaluateOverload(conn, stats))
	})

	t.Run("nil cluster health not overloaded", func(t *testing.T) {
		c := makeClient()
		conn := makeConn()

		stats := &NodeStats{
			JVM:      JVMStats{Mem: JVMMemStats{HeapUsedPercent: 50}},
			Breakers: map[string]BreakerStats{},
		}

		require.False(t, c.evaluateOverload(conn, stats))
	})

	t.Run("multiple conditions", func(t *testing.T) {
		c := makeClient()
		conn := makeConn()
		conn.mu.Lock()
		conn.mu.clusterHealth = &ClusterHealthLocal{Status: "red"}
		conn.mu.Unlock()

		stats := &NodeStats{
			JVM: JVMStats{Mem: JVMMemStats{HeapUsedPercent: 90}},
			Breakers: map[string]BreakerStats{
				"parent": {LimitSizeInBytes: 1000, EstimatedSizeInBytes: 950, Tripped: 0},
			},
		}

		require.True(t, c.evaluateOverload(conn, stats))
	})

	t.Run("first poll initializes lastBreakerTripped", func(t *testing.T) {
		c := makeClient()
		conn := makeConn()

		stats := &NodeStats{
			JVM: JVMStats{Mem: JVMMemStats{HeapUsedPercent: 50}},
			Breakers: map[string]BreakerStats{
				"fielddata": {LimitSizeInBytes: 1000, EstimatedSizeInBytes: 100, Tripped: 3},
				"request":   {LimitSizeInBytes: 2000, EstimatedSizeInBytes: 200, Tripped: 1},
			},
		}

		conn.mu.RLock()
		require.Nil(t, conn.mu.lastBreakerTripped)
		conn.mu.RUnlock()

		c.evaluateOverload(conn, stats)

		conn.mu.RLock()
		require.NotNil(t, conn.mu.lastBreakerTripped)
		require.Equal(t, int64(3), conn.mu.lastBreakerTripped["fielddata"])
		require.Equal(t, int64(1), conn.mu.lastBreakerTripped["request"])
		conn.mu.RUnlock()
	})
}

func TestConnectionClusterHealth(t *testing.T) {
	t.Run("nil when not set", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{Host: "node:9200"}}
		require.Nil(t, conn.ClusterHealth())
	})

	t.Run("returns populated health", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{Host: "node:9200"}}
		expected := &ClusterHealthLocal{
			ClusterName:   "test-cluster",
			Status:        "green",
			NumberOfNodes: 3,
		}
		conn.mu.Lock()
		conn.mu.clusterHealth = expected
		conn.mu.Unlock()

		result := conn.ClusterHealth()
		require.NotNil(t, result)
		require.Equal(t, "test-cluster", result.ClusterName)
		require.Equal(t, "green", result.Status)
		require.Equal(t, 3, result.NumberOfNodes)
	})
}

func TestFetchAndEvaluateNodeStats(t *testing.T) {
	makeStatsResponse := func(heapPct int, breakers map[string]BreakerStats) string {
		stats := NodeStatsResponse{
			Nodes: map[string]NodeStats{
				"testnode": {
					JVM:      JVMStats{Mem: JVMMemStats{HeapUsedPercent: heapPct}},
					Breakers: breakers,
				},
			},
		}
		b, err := json.Marshal(stats)
		if err != nil {
			panic(err)
		}
		return string(b)
	}

	t.Run("healthy stats no action", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(makeStatsResponse(30, nil)))
		}))
		defer server.Close()

		serverURL, _ := url.Parse(server.URL)
		conn := &Connection{URL: serverURL}
		conn.state.Store(int64(newConnState(lcActive)))
		pool := newStandbyPool([]*Connection{conn}, nil)

		client := newTestClientWithPool(server.Client().Transport, pool)
		client.fetchAndEvaluateNodeStats(conn, pool)

		// No overload -> connection should remain active
		require.True(t, conn.loadConnState().lifecycle().has(lcActive))
	})

	t.Run("high heap triggers overload demotion", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(makeStatsResponse(95, nil)))
		}))
		defer server.Close()

		serverURL, _ := url.Parse(server.URL)
		a1 := &Connection{URL: serverURL}
		a1.state.Store(int64(newConnState(lcActive)))
		s1 := newStandbyConn("backup")
		pool := newStandbyPool([]*Connection{a1}, []*Connection{s1})

		client := newTestClientWithPool(server.Client().Transport, pool)
		client.overloadedHeapThreshold = 85
		client.fetchAndEvaluateNodeStats(a1, pool)

		// High heap -> should be demoted to standby with overloaded flag
		require.True(t, a1.loadConnState().lifecycle().has(lcOverloaded))
	})

	t.Run("non-200 status is ignored", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer server.Close()

		serverURL, _ := url.Parse(server.URL)
		conn := &Connection{URL: serverURL}
		conn.state.Store(int64(newConnState(lcActive)))
		pool := newStandbyPool([]*Connection{conn}, nil)

		client := newTestClientWithPool(server.Client().Transport, pool)
		client.fetchAndEvaluateNodeStats(conn, pool)

		// Non-200 -> no action, connection stays active
		require.True(t, conn.loadConnState().lifecycle().has(lcActive))
	})

	t.Run("network error with overloaded conn clears overload", func(t *testing.T) {
		// Server that immediately closes connection
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hj, ok := w.(http.Hijacker)
			if !ok {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			conn, _, _ := hj.Hijack()
			conn.Close()
		}))
		defer server.Close()

		serverURL, _ := url.Parse(server.URL)
		conn := &Connection{URL: serverURL}
		conn.state.Store(int64(newConnState(lcActive | lcOverloaded)))

		pool := newStandbyPool([]*Connection{conn}, nil)

		client := newTestClientWithPool(server.Client().Transport, pool)
		client.fetchAndEvaluateNodeStats(conn, pool)

		// Network error + was overloaded -> should transition away from overloaded
		lc := conn.loadConnState().lifecycle()
		require.False(t, lc.has(lcOverloaded), "overloaded flag should be cleared after network error")
	})

	t.Run("overloaded conn cleared when stats improve", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(makeStatsResponse(30, nil))) // Low heap
		}))
		defer server.Close()

		serverURL, _ := url.Parse(server.URL)
		conn := &Connection{URL: serverURL}
		conn.state.Store(int64(newConnState(lcStandby | lcOverloaded)))
		conn.mu.Lock()
		conn.mu.overloadedAt = time.Now()
		conn.mu.Unlock()

		a1 := newActiveConn("a1")
		pool := newStandbyPool([]*Connection{a1}, []*Connection{conn})

		client := newTestClientWithPool(server.Client().Transport, pool)
		client.overloadedHeapThreshold = 85
		client.fetchAndEvaluateNodeStats(conn, pool)

		// Stats are healthy -> overloaded flag should be cleared
		require.False(t, conn.loadConnState().lifecycle().has(lcOverloaded))
	})

	t.Run("empty nodes map is handled", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"nodes":{}}`))
		}))
		defer server.Close()

		serverURL, _ := url.Parse(server.URL)
		conn := &Connection{URL: serverURL}
		conn.state.Store(int64(newConnState(lcActive)))
		pool := newStandbyPool([]*Connection{conn}, nil)

		client := newTestClientWithPool(server.Client().Transport, pool)
		client.fetchAndEvaluateNodeStats(conn, pool)

		// Empty nodes -> no action
		require.True(t, conn.loadConnState().lifecycle().has(lcActive))
	})

	t.Run("invalid JSON body is handled", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`not json`))
		}))
		defer server.Close()

		serverURL, _ := url.Parse(server.URL)
		conn := &Connection{URL: serverURL}
		conn.state.Store(int64(newConnState(lcActive)))
		pool := newStandbyPool([]*Connection{conn}, nil)

		client := newTestClientWithPool(server.Client().Transport, pool)
		client.fetchAndEvaluateNodeStats(conn, pool)

		// Invalid JSON -> no action
		require.True(t, conn.loadConnState().lifecycle().has(lcActive))
	})
}

func TestPollClusterHealthMultiNode(t *testing.T) {
	t.Run("refreshes health for multi-node pool connections", func(t *testing.T) {
		var requestCount atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"cluster_name": "poll-cluster",
				"status": "green",
				"number_of_nodes": 2
			}`))
		}))
		defer server.Close()

		serverURL, _ := url.Parse(server.URL)
		conn1 := &Connection{URL: serverURL}
		conn1.state.Store(int64(newConnState(lcActive)))
		conn1.clusterHealthState.Store(int64(clusterHealthProbed | clusterHealthAvailable))
		conn2 := &Connection{URL: serverURL}
		conn2.state.Store(int64(newConnState(lcActive)))
		conn2.clusterHealthState.Store(int64(clusterHealthProbed | clusterHealthAvailable))

		pool := newStandbyPool([]*Connection{conn1, conn2}, nil)
		client := newTestClientWithPool(server.Client().Transport, pool)

		client.pollClusterHealth()

		require.Equal(t, int64(2), requestCount.Load())
	})

	t.Run("skips when pool has default type", func(t *testing.T) {
		client := newTestClientWithPool(http.DefaultTransport, nil)
		// Should not panic with nil pool
		client.pollClusterHealth()
	})
}

func TestSnapshotClusterHealthConnectionsEdgeCases(t *testing.T) {
	t.Run("nil pool returns nil", func(t *testing.T) {
		client := newTestClientWithPool(http.DefaultTransport, nil)
		result := client.snapshotClusterHealthConnections()
		require.Nil(t, result)
	})

	t.Run("singleServerPool returns nil", func(t *testing.T) {
		conn := &Connection{URL: &url.URL{Host: "single:9200"}}
		pool := &singleServerPool{connection: conn}
		client := newTestClientWithPool(http.DefaultTransport, pool)
		result := client.snapshotClusterHealthConnections()
		require.Nil(t, result)
	})
}

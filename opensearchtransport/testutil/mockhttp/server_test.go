// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mockhttp_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil/mockhttp"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestNewServerPool_ValidCount(t *testing.T) {
	pool, err := mockhttp.NewServerPool(t, "test-valid", 3)
	require.NoError(t, err)
	require.Equal(t, 3, pool.Count())
}

func TestNewServerPool_ZeroCountReturnsError(t *testing.T) {
	pool, err := mockhttp.NewServerPool(t, "test-zero", 0)
	require.Error(t, err)
	require.Nil(t, pool)
}

func TestNewServerPool_NegativeCountReturnsError(t *testing.T) {
	pool, err := mockhttp.NewServerPool(t, "test-neg", -1)
	require.Error(t, err)
	require.Nil(t, pool)
}

func TestNewServerPool_URLsAndPortsAllocated(t *testing.T) {
	pool, err := mockhttp.NewServerPool(t, "test-urls", 2)
	require.NoError(t, err)

	urls := pool.URLs()
	require.Len(t, urls, 2)
	for _, u := range urls {
		require.NotNil(t, u)
		require.Equal(t, "http", u.Scheme)
	}

	ports := pool.Ports()
	require.Len(t, ports, 2)
	for _, p := range ports {
		require.Positive(t, p)
	}
}

func TestStartStop_AllServersAccessible(t *testing.T) {
	pool, err := mockhttp.NewServerPool(t, "test-start-stop", 2)
	require.NoError(t, err)

	err = pool.Start(t, okHandler())
	require.NoError(t, err)

	// Verify all servers are accessible
	client := &http.Client{Timeout: 2 * time.Second}
	for _, u := range pool.URLs() {
		resp, httpErr := client.Get(u.String())
		require.NoError(t, httpErr)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	// Stop and verify unreachable
	pool.Stop(t)
	for _, u := range pool.URLs() {
		resp, httpErr := client.Get(u.String())
		if resp != nil {
			resp.Body.Close()
		}
		require.Error(t, httpErr)
	}
}

func TestStartStopServer_IndividualLifecycle(t *testing.T) {
	pool, err := mockhttp.NewServerPool(t, "test-individual", 2)
	require.NoError(t, err)

	// Start only server 0
	err = pool.StartServer(t, 0, okHandler())
	require.NoError(t, err)
	require.True(t, pool.IsActive(0))
	require.False(t, pool.IsActive(1))

	// Stop server 0
	pool.StopServer(t, 0)
	require.False(t, pool.IsActive(0))
}

func TestStartServer_AlreadyRunningReturnsError(t *testing.T) {
	pool, err := mockhttp.NewServerPool(t, "test-already-running", 1)
	require.NoError(t, err)

	err = pool.StartServer(t, 0, okHandler())
	require.NoError(t, err)

	err = pool.StartServer(t, 0, okHandler())
	require.Error(t, err)
	require.Contains(t, err.Error(), "already running")
}

func TestStartServer_OutOfRangeReturnsError(t *testing.T) {
	pool, err := mockhttp.NewServerPool(t, "test-oor-start", 1)
	require.NoError(t, err)

	err = pool.StartServer(t, 5, okHandler())
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")
}

func TestRestartServer_NewHandlerIsActive(t *testing.T) {
	pool, err := mockhttp.NewServerPool(t, "test-restart", 1)
	require.NoError(t, err)

	err = pool.StartServer(t, 0, okHandler())
	require.NoError(t, err)

	// Restart with a handler that returns 200 but with a custom header
	// to distinguish from the original handler. The handler must return
	// 200 because WaitForServerReady checks for StatusOK.
	newHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Restarted", "true")
		w.WriteHeader(http.StatusOK)
	})
	err = pool.RestartServer(t, 0, newHandler)
	require.NoError(t, err)
	require.True(t, pool.IsActive(0))

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(pool.URLs()[0].String())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "true", resp.Header.Get("X-Restarted"))
	resp.Body.Close()
}

func TestGetServer_ValidIndex(t *testing.T) {
	pool, err := mockhttp.NewServerPool(t, "test-get", 2)
	require.NoError(t, err)

	srv, err := pool.GetServer(0)
	require.NoError(t, err)
	require.NotNil(t, srv)

	srv, err = pool.GetServer(1)
	require.NoError(t, err)
	require.NotNil(t, srv)
}

func TestGetServer_OutOfRangeReturnsError(t *testing.T) {
	pool, err := mockhttp.NewServerPool(t, "test-get-oor", 1)
	require.NoError(t, err)

	_, err = pool.GetServer(5)
	require.Error(t, err)
	require.Contains(t, err.Error(), "out of range")

	_, err = pool.GetServer(-1)
	require.Error(t, err)
}

func TestIsActive_BeforeAndAfterStart(t *testing.T) {
	pool, err := mockhttp.NewServerPool(t, "test-active", 1)
	require.NoError(t, err)

	// Before start
	require.False(t, pool.IsActive(0))

	// Out of range returns false
	require.False(t, pool.IsActive(99))

	// After start
	err = pool.StartServer(t, 0, okHandler())
	require.NoError(t, err)
	require.True(t, pool.IsActive(0))
}

func TestCount_MatchesCreationCount(t *testing.T) {
	for _, count := range []int{1, 3, 5} {
		t.Run(fmt.Sprintf("count=%d", count), func(t *testing.T) {
			pool, err := mockhttp.NewServerPool(t, fmt.Sprintf("test-count-%d", count), count)
			require.NoError(t, err)
			require.Equal(t, count, pool.Count())
		})
	}
}

func TestServerAccessors(t *testing.T) {
	pool, err := mockhttp.NewServerPool(t, "test-accessors", 1)
	require.NoError(t, err)

	srv, err := pool.GetServer(0)
	require.NoError(t, err)

	require.NotNil(t, srv.URL())
	require.Equal(t, "http", srv.URL().Scheme)
	require.Positive(t, srv.Port())
	require.Contains(t, srv.Name(), "test-accessors")
	require.False(t, srv.IsActive())

	// Start and check IsActive changes
	err = pool.StartServer(t, 0, okHandler())
	require.NoError(t, err)
	require.True(t, srv.IsActive())
}

func TestWaitForServerReady_ServerUp(t *testing.T) {
	pool, err := mockhttp.NewServerPool(t, "test-ready-up", 1)
	require.NoError(t, err)

	err = pool.StartServer(t, 0, okHandler())
	require.NoError(t, err)

	srv, err := pool.GetServer(0)
	require.NoError(t, err)

	addr := fmt.Sprintf("localhost:%d", srv.Port())
	err = mockhttp.WaitForServerReady(t, addr, 5*time.Second)
	require.NoError(t, err)
}

func TestWaitForServerReady_ServerDownReturnsError(t *testing.T) {
	// Use a port that is not serving
	err := mockhttp.WaitForServerReady(t, "localhost:19999", 500*time.Millisecond)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not ready")
}

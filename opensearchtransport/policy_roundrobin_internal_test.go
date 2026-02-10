// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRoundRobinPolicy(t *testing.T) {
	t.Run("NewRoundRobinPolicy creates policy", func(t *testing.T) {
		policy := NewRoundRobinPolicy()
		require.NotNil(t, policy)
		require.IsType(t, &RoundRobinPolicy{}, policy)
	})

	t.Run("IsEnabled returns false when no pool", func(t *testing.T) {
		policy := NewRoundRobinPolicy()
		require.False(t, policy.IsEnabled())
	})

	t.Run("IsEnabled returns false when pool has no connections", func(t *testing.T) {
		policy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		policy.configurePolicySettings(createTestConfig())
		require.False(t, policy.IsEnabled())
	})

	t.Run("IsEnabled returns true with live connections", func(t *testing.T) {
		policy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		policy.configurePolicySettings(createTestConfig())

		conn := createTestConnection("http://localhost:9200")
		err := policy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)

		// Connection starts dead, so IsEnabled is false
		require.False(t, policy.IsEnabled())

		// Get connection via Next (tryZombie) and promote it to live
		zombieConn, err := policy.pool.Next()
		require.NoError(t, err)
		require.NotNil(t, zombieConn, "Next() should return a zombie connection")
		policy.pool.OnSuccess(zombieConn)

		// Now IsEnabled should be true
		require.True(t, policy.IsEnabled())
	})

	t.Run("Eval returns nil when no pool", func(t *testing.T) {
		policy := NewRoundRobinPolicy()
		ctx := context.Background()
		req, _ := http.NewRequest("GET", "/", nil)

		pool, err := policy.Eval(ctx, req)
		require.Nil(t, pool)
		require.Nil(t, err)
	})

	t.Run("Eval returns pool when configured", func(t *testing.T) {
		policy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		policy.configurePolicySettings(createTestConfig())

		ctx := context.Background()
		req, _ := http.NewRequest("GET", "/", nil)

		pool, err := policy.Eval(ctx, req)
		require.NotNil(t, pool)
		require.NoError(t, err)
	})

	t.Run("DiscoveryUpdate with nil changes is no-op", func(t *testing.T) {
		policy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		policy.configurePolicySettings(createTestConfig())

		err := policy.DiscoveryUpdate(nil, nil, nil)
		require.NoError(t, err)
	})

	t.Run("DiscoveryUpdate adds all connections", func(t *testing.T) {
		policy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		policy.configurePolicySettings(createTestConfig())

		conn1 := createTestConnection("http://localhost:9200", RoleData)
		conn2 := createTestConnection("http://localhost:9201", RoleIngest)
		conn3 := createTestConnection("http://localhost:9202", RoleCoordinatingOnly)

		err := policy.DiscoveryUpdate([]*Connection{conn1, conn2, conn3}, nil, nil)
		require.NoError(t, err)
		require.Len(t, policy.pool.connections(), 3)
	})

	t.Run("DiscoveryUpdate removes connections", func(t *testing.T) {
		policy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		policy.configurePolicySettings(createTestConfig())

		conn1 := createTestConnection("http://localhost:9200")
		conn2 := createTestConnection("http://localhost:9201")

		err := policy.DiscoveryUpdate([]*Connection{conn1, conn2}, nil, nil)
		require.NoError(t, err)
		require.Len(t, policy.pool.connections(), 2)

		err = policy.DiscoveryUpdate(nil, []*Connection{conn1}, nil)
		require.NoError(t, err)
		require.Len(t, policy.pool.connections(), 1)
	})

	t.Run("DiscoveryUpdate with empty removed list", func(t *testing.T) {
		policy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		policy.configurePolicySettings(createTestConfig())

		conn := createTestConnection("http://localhost:9200")
		err := policy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)

		// Empty removed list should be no-op
		err = policy.DiscoveryUpdate(nil, []*Connection{}, nil)
		require.NoError(t, err)
		require.Len(t, policy.pool.connections(), 1)
	})

	t.Run("CheckDead with nil pool is no-op", func(t *testing.T) {
		policy := NewRoundRobinPolicy()
		ctx := context.Background()
		healthCheck := func(ctx context.Context, u *url.URL) (*http.Response, error) { return nil, nil }
		err := policy.CheckDead(ctx, healthCheck)
		require.NoError(t, err)
	})

	t.Run("CheckDead delegates to pool", func(t *testing.T) {
		policy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		policy.configurePolicySettings(createTestConfig())

		// Create a test server that returns success
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
		}))
		defer server.Close()

		// Add a dead connection that needs health checking
		conn := createTestConnection(server.URL)
		policy.DiscoveryUpdate([]*Connection{conn}, nil, nil)

		// Move connection from dead to the checkable state
		policy.pool.mu.Lock()
		if len(policy.pool.mu.dead) > 0 {
			conn := policy.pool.mu.dead[0]
			conn.mu.Lock()
			conn.mu.deadSince = time.Now().Add(-2 * time.Minute) // Make it eligible for checking
			conn.mu.Unlock()
		}
		policy.pool.mu.Unlock()

		ctx := context.Background()
		called := false
		healthCheck := func(ctx context.Context, u *url.URL) (*http.Response, error) {
			called = true
			// Perform actual HTTP call to test server
			req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
			if err != nil {
				return nil, err
			}
			return http.DefaultClient.Do(req)
		}

		err := policy.CheckDead(ctx, healthCheck)
		require.NoError(t, err)
		require.True(t, called, "healthCheck should be called for dead connections")
	})

	t.Run("configurePolicySettings creates pool only once", func(t *testing.T) {
		policy := NewRoundRobinPolicy().(*RoundRobinPolicy)

		err := policy.configurePolicySettings(createTestConfig())
		require.NoError(t, err)
		pool1 := policy.pool

		err = policy.configurePolicySettings(createTestConfig())
		require.NoError(t, err)
		pool2 := policy.pool

		require.Same(t, pool1, pool2, "Pool should not be recreated")
	})
}

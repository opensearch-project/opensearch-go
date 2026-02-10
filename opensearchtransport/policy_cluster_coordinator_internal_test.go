// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCoordinatorPolicy(t *testing.T) {
	t.Run("NewCoordinatorPolicy creates policy", func(t *testing.T) {
		policy := NewCoordinatorPolicy()
		require.NotNil(t, policy)
		require.IsType(t, &CoordinatorPolicy{}, policy)
	})

	t.Run("IsEnabled returns false initially", func(t *testing.T) {
		policy := NewCoordinatorPolicy()
		require.False(t, policy.IsEnabled())
	})

	t.Run("IsEnabled returns true after adding coordinating nodes", func(t *testing.T) {
		policy := NewCoordinatorPolicy().(*CoordinatorPolicy)
		policy.configurePolicySettings(createTestConfig())

		conn := createTestConnection("http://localhost:9200", RoleCoordinatingOnly)
		err := policy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)

		require.True(t, policy.IsEnabled())
	})

	t.Run("IsEnabled returns true after adding coordinating nodes with explicit role", func(t *testing.T) {
		policy := NewCoordinatorPolicy().(*CoordinatorPolicy)
		policy.configurePolicySettings(createTestConfig())

		conn := createTestConnection("http://localhost:9200", RoleCoordinatingOnly)
		err := policy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)

		require.True(t, policy.IsEnabled())
	})

	t.Run("Eval returns nil when no coordinators", func(t *testing.T) {
		policy := NewCoordinatorPolicy()
		ctx := context.Background()
		req, _ := http.NewRequest(http.MethodGet, "/", nil)

		pool, err := policy.Eval(ctx, req)
		require.Nil(t, pool)
		require.Nil(t, err)
	})

	t.Run("Eval returns pool when coordinators available", func(t *testing.T) {
		policy := NewCoordinatorPolicy().(*CoordinatorPolicy)
		policy.configurePolicySettings(createTestConfig())

		conn := createTestConnection("http://localhost:9200", RoleCoordinatingOnly)
		err := policy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)

		ctx := context.Background()
		req, _ := http.NewRequest(http.MethodGet, "/", nil)

		pool, err := policy.Eval(ctx, req)
		require.NotNil(t, pool)
		require.NoError(t, err)
	})

	t.Run("DiscoveryUpdate with nil changes is no-op", func(t *testing.T) {
		policy := NewCoordinatorPolicy().(*CoordinatorPolicy)
		policy.configurePolicySettings(createTestConfig())

		err := policy.DiscoveryUpdate(nil, nil, nil)
		require.NoError(t, err)
	})

	t.Run("DiscoveryUpdate filters non-coordinating nodes", func(t *testing.T) {
		policy := NewCoordinatorPolicy().(*CoordinatorPolicy)
		policy.configurePolicySettings(createTestConfig())

		coordConn := createTestConnection("http://localhost:9200", RoleCoordinatingOnly)
		dataConn := createTestConnection("http://localhost:9201", RoleData)
		ingestConn := createTestConnection("http://localhost:9202", RoleIngest)

		err := policy.DiscoveryUpdate([]*Connection{coordConn, dataConn, ingestConn}, nil, nil)
		require.NoError(t, err)

		// Only coordinator should be added
		require.True(t, policy.IsEnabled())
	})

	t.Run("DiscoveryUpdate removes connections", func(t *testing.T) {
		policy := NewCoordinatorPolicy().(*CoordinatorPolicy)
		policy.configurePolicySettings(createTestConfig())

		conn := createTestConnection("http://localhost:9200", RoleCoordinatingOnly)
		err := policy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)
		require.True(t, policy.IsEnabled())

		err = policy.DiscoveryUpdate(nil, []*Connection{conn}, nil)
		require.NoError(t, err)
		require.False(t, policy.IsEnabled())
	})

	t.Run("DiscoveryUpdate with both live and dead coordinators", func(t *testing.T) {
		policy := NewCoordinatorPolicy().(*CoordinatorPolicy)
		policy.configurePolicySettings(createTestConfig())

		conn1 := createTestConnection("http://localhost:9200", RoleCoordinatingOnly)
		conn2 := createTestConnection("http://localhost:9201", RoleCoordinatingOnly)

		err := policy.DiscoveryUpdate([]*Connection{conn1, conn2}, nil, nil)
		require.NoError(t, err)
		require.True(t, policy.IsEnabled())
	})

	t.Run("CheckDead with nil pool is no-op", func(t *testing.T) {
		policy := NewCoordinatorPolicy()
		ctx := context.Background()
		healthCheck := func(ctx context.Context, u *url.URL) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK}, nil
		}
		err := policy.CheckDead(ctx, healthCheck)
		require.NoError(t, err)
	})

	t.Run("CheckDead delegates to pool", func(t *testing.T) {
		policy := NewCoordinatorPolicy().(*CoordinatorPolicy)
		policy.configurePolicySettings(createTestConfig())

		ctx := context.Background()
		healthCheck := func(ctx context.Context, u *url.URL) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK}, nil
		}

		err := policy.CheckDead(ctx, healthCheck)
		require.NoError(t, err)
	})

	t.Run("configurePolicySettings creates pool only once", func(t *testing.T) {
		policy := NewCoordinatorPolicy().(*CoordinatorPolicy)

		err := policy.configurePolicySettings(createTestConfig())
		require.NoError(t, err)
		pool1 := policy.pool

		err = policy.configurePolicySettings(createTestConfig())
		require.NoError(t, err)
		pool2 := policy.pool

		require.Same(t, pool1, pool2, "Pool should not be recreated")
	})
}

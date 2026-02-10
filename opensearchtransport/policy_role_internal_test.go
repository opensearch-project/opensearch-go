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

func TestRolePolicy(t *testing.T) {
	t.Run("NewRolePolicy with valid roles", func(t *testing.T) {
		policy, err := NewRolePolicy(RoleData)
		require.NoError(t, err)
		require.NotNil(t, policy)
		require.IsType(t, &RolePolicy{}, policy)
	})

	t.Run("NewRolePolicy with multiple roles", func(t *testing.T) {
		policy, err := NewRolePolicy(RoleData, RoleIngest)
		require.NoError(t, err)
		require.NotNil(t, policy)
	})

	t.Run("NewRolePolicy with no roles returns error", func(t *testing.T) {
		policy, err := NewRolePolicy()
		require.Error(t, err)
		require.Nil(t, policy)
		require.Contains(t, err.Error(), "no roles specified")
	})

	t.Run("NewRolePolicy with invalid role separator returns error", func(t *testing.T) {
		policy, err := NewRolePolicy("data,ingest")
		require.Error(t, err)
		require.Nil(t, policy)
		require.IsType(t, InvalidRoleError{}, err)
	})

	t.Run("RequiredRoleKey returns normalized key", func(t *testing.T) {
		policy, err := NewRolePolicy(RoleData, RoleIngest)
		require.NoError(t, err)

		rolePolicy := policy.(*RolePolicy)
		key := rolePolicy.RequiredRoleKey()
		require.Contains(t, key, RoleData)
		require.Contains(t, key, RoleIngest)
		require.Equal(t, "data,ingest", key) // Sorted
	})

	t.Run("IsEnabled returns false initially", func(t *testing.T) {
		policy, err := NewRolePolicy(RoleData)
		require.NoError(t, err)
		require.False(t, policy.IsEnabled())
	})

	t.Run("IsEnabled returns true after adding matching connections", func(t *testing.T) {
		policy, err := NewRolePolicy(RoleData)
		require.NoError(t, err)

		rolePolicy := policy.(*RolePolicy)
		rolePolicy.configurePolicySettings(createTestConfig())

		conn := createTestConnection("http://localhost:9200", RoleData)
		err = rolePolicy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)

		require.True(t, policy.IsEnabled())
	})

	t.Run("Eval returns nil when no matching connections", func(t *testing.T) {
		policy, err := NewRolePolicy(RoleData)
		require.NoError(t, err)

		ctx := context.Background()
		req, _ := http.NewRequest("GET", "/", nil)

		pool, err := policy.Eval(ctx, req)
		require.Nil(t, pool)
		require.Nil(t, err)
	})

	t.Run("Eval returns pool when matching connections available", func(t *testing.T) {
		policy, err := NewRolePolicy(RoleData)
		require.NoError(t, err)

		rolePolicy := policy.(*RolePolicy)
		rolePolicy.configurePolicySettings(createTestConfig())

		conn := createTestConnection("http://localhost:9200", RoleData)
		err = rolePolicy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)

		ctx := context.Background()
		req, _ := http.NewRequest("GET", "/", nil)

		pool, err := policy.Eval(ctx, req)
		require.NotNil(t, pool)
		require.NoError(t, err)
	})

	t.Run("DiscoveryUpdate filters non-matching roles", func(t *testing.T) {
		policy, err := NewRolePolicy(RoleData)
		require.NoError(t, err)

		rolePolicy := policy.(*RolePolicy)
		rolePolicy.configurePolicySettings(createTestConfig())

		dataConn := createTestConnection("http://localhost:9200", RoleData)
		ingestConn := createTestConnection("http://localhost:9201", RoleIngest)
		clusterManagerConn := createTestConnection("http://localhost:9202", RoleClusterManager)

		err = rolePolicy.DiscoveryUpdate([]*Connection{dataConn, ingestConn, clusterManagerConn}, nil, nil)
		require.NoError(t, err)

		// Only data node should be added
		require.True(t, policy.IsEnabled())
	})

	t.Run("DiscoveryUpdate requires all roles", func(t *testing.T) {
		policy, err := NewRolePolicy(RoleData, RoleIngest)
		require.NoError(t, err)

		rolePolicy := policy.(*RolePolicy)
		rolePolicy.configurePolicySettings(createTestConfig())

		dataOnlyConn := createTestConnection("http://localhost:9200", RoleData)
		ingestOnlyConn := createTestConnection("http://localhost:9201", RoleIngest)
		bothRolesConn := createTestConnection("http://localhost:9202", RoleData, RoleIngest)

		err = rolePolicy.DiscoveryUpdate([]*Connection{dataOnlyConn, ingestOnlyConn, bothRolesConn}, nil, nil)
		require.NoError(t, err)

		// Only the node with both roles should be added
		require.True(t, policy.IsEnabled())
	})

	t.Run("DiscoveryUpdate removes connections", func(t *testing.T) {
		policy, err := NewRolePolicy(RoleData)
		require.NoError(t, err)

		rolePolicy := policy.(*RolePolicy)
		rolePolicy.configurePolicySettings(createTestConfig())

		conn := createTestConnection("http://localhost:9200", RoleData)
		err = rolePolicy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)
		require.True(t, policy.IsEnabled())

		err = rolePolicy.DiscoveryUpdate(nil, []*Connection{conn}, nil)
		require.NoError(t, err)
		require.False(t, policy.IsEnabled())
	})

	t.Run("DiscoveryUpdate with no changes to remove", func(t *testing.T) {
		policy, err := NewRolePolicy(RoleData)
		require.NoError(t, err)

		rolePolicy := policy.(*RolePolicy)
		rolePolicy.configurePolicySettings(createTestConfig())

		conn := createTestConnection("http://localhost:9200", RoleData)
		err = rolePolicy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)

		// Try to remove a different connection (early exit path)
		otherConn := createTestConnection("http://localhost:9201", RoleData)
		err = rolePolicy.DiscoveryUpdate(nil, []*Connection{otherConn}, nil)
		require.NoError(t, err)
		require.True(t, policy.IsEnabled()) // Original connection still there
	})

	t.Run("DiscoveryUpdate handles coordinating-only role", func(t *testing.T) {
		policy, err := NewRolePolicy(RoleCoordinatingOnly)
		require.NoError(t, err)

		rolePolicy := policy.(*RolePolicy)
		rolePolicy.configurePolicySettings(createTestConfig())

		// Empty roles = coordinating-only
		emptyRolesConn := createTestConnection("http://localhost:9200")
		explicitCoordConn := createTestConnection("http://localhost:9201", RoleCoordinatingOnly)
		dataConn := createTestConnection("http://localhost:9202", RoleData)

		err = rolePolicy.DiscoveryUpdate([]*Connection{emptyRolesConn, explicitCoordConn, dataConn}, nil, nil)
		require.NoError(t, err)

		require.True(t, policy.IsEnabled())
	})

	t.Run("connectionMatchesRoles with empty role key", func(t *testing.T) {
		policy := &RolePolicy{requiredRoleKey: ""}
		policy.configurePolicySettings(createTestConfig())

		conn := createTestConnection("http://localhost:9200", RoleData)
		require.False(t, policy.connectionMatchesRoles(conn))
	})

	t.Run("CheckDead delegates to pool", func(t *testing.T) {
		policy, err := NewRolePolicy(RoleData)
		require.NoError(t, err)

		rolePolicy := policy.(*RolePolicy)
		rolePolicy.configurePolicySettings(createTestConfig())

		ctx := context.Background()
		healthCheck := func(ctx context.Context, u *url.URL) (*http.Response, error) { return nil, nil }

		err = rolePolicy.CheckDead(ctx, healthCheck)
		require.NoError(t, err)
	})

	t.Run("configurePolicySettings creates pool only once", func(t *testing.T) {
		policy, err := NewRolePolicy(RoleData)
		require.NoError(t, err)

		rolePolicy := policy.(*RolePolicy)

		err = rolePolicy.configurePolicySettings(createTestConfig())
		require.NoError(t, err)
		pool1 := rolePolicy.pool

		err = rolePolicy.configurePolicySettings(createTestConfig())
		require.NoError(t, err)
		pool2 := rolePolicy.pool

		require.Same(t, pool1, pool2, "Pool should not be recreated")
	})

	t.Run("mustRolePolicy panics on error", func(t *testing.T) {
		require.Panics(t, func() {
			mustRolePolicy("invalid,role")
		})
	})

	t.Run("mustRolePolicy succeeds with valid role", func(t *testing.T) {
		require.NotPanics(t, func() {
			policy := mustRolePolicy(RoleData)
			require.NotNil(t, policy)
		})
	})
}

func TestNormalizeRoles(t *testing.T) {
	t.Run("empty roles returns empty string", func(t *testing.T) {
		result, err := NormalizeRoles([]string{})
		require.NoError(t, err)
		require.Equal(t, "", result)
	})

	t.Run("nil roles returns empty string", func(t *testing.T) {
		result, err := NormalizeRoles(nil)
		require.NoError(t, err)
		require.Equal(t, "", result)
	})

	t.Run("single role", func(t *testing.T) {
		result, err := NormalizeRoles([]string{"data"})
		require.NoError(t, err)
		require.Equal(t, "data", result)
	})

	t.Run("multiple roles are sorted", func(t *testing.T) {
		result, err := NormalizeRoles([]string{"ingest", "data"})
		require.NoError(t, err)
		require.Equal(t, "data,ingest", result)
	})

	t.Run("duplicate roles are deduplicated", func(t *testing.T) {
		result, err := NormalizeRoles([]string{"data", "data", "ingest"})
		require.NoError(t, err)
		require.Equal(t, "data,ingest", result)
	})

	t.Run("three roles sorted", func(t *testing.T) {
		result, err := NormalizeRoles([]string{"ingest", "cluster_manager", "data"})
		require.NoError(t, err)
		require.Equal(t, "cluster_manager,data,ingest", result)
	})

	t.Run("role with separator returns error", func(t *testing.T) {
		result, err := NormalizeRoles([]string{"data,ingest"})
		require.Error(t, err)
		require.Equal(t, "", result)
		require.IsType(t, InvalidRoleError{}, err)
	})

	t.Run("first role valid, second invalid", func(t *testing.T) {
		result, err := NormalizeRoles([]string{"data", "bad,role"})
		require.Error(t, err)
		require.Equal(t, "", result)
		require.IsType(t, InvalidRoleError{}, err)
	})
}

func TestInvalidRoleError(t *testing.T) {
	t.Run("Error message formatting", func(t *testing.T) {
		err := InvalidRoleError{Role: "bad,role", Separator: ","}
		require.Contains(t, err.Error(), "bad,role")
		require.Contains(t, err.Error(), ",")
		require.Contains(t, err.Error(), "cannot contain")
	})

	t.Run("Error message with different separator", func(t *testing.T) {
		err := InvalidRoleError{Role: "bad;role", Separator: ";"}
		require.Contains(t, err.Error(), "bad;role")
		require.Contains(t, err.Error(), ";")
	})
}

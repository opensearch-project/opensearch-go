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
	"sync"
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
		var invalidRoleErr InvalidRoleError
		require.ErrorAs(t, err, &invalidRoleErr)
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
		req, _ := http.NewRequest(http.MethodGet, "/", nil)

		hop, err := policy.Eval(ctx, req)
		require.Nil(t, hop.Conn)
		require.NoError(t, err)
	})

	t.Run("Eval returns conn when matching connections available", func(t *testing.T) {
		policy, err := NewRolePolicy(RoleData)
		require.NoError(t, err)

		rolePolicy := policy.(*RolePolicy)
		rolePolicy.configurePolicySettings(createTestConfig())

		conn := createTestConnection("http://localhost:9200", RoleData)
		err = rolePolicy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)

		ctx := context.Background()
		req, _ := http.NewRequest(http.MethodGet, "/", nil)

		hop, err := policy.Eval(ctx, req)
		require.NotNil(t, hop.Conn)
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

	t.Run("DiscoveryUpdate role-filters dead connections", func(t *testing.T) {
		// A connection can arrive in the "added" list already classified as
		// unhealthy (the allConns pool placed it on its dead list). RolePolicy
		// must still apply role filtering to such connections: a dead conn that
		// matches is admitted to the pool's dead list for later health checks,
		// while a dead conn that does not match is dropped entirely. This is the
		// scenario createDeadTestConnection's roles argument exists for.
		policy, err := NewRolePolicy(RoleData)
		require.NoError(t, err)

		rolePolicy := policy.(*RolePolicy)
		rolePolicy.configurePolicySettings(createTestConfig())

		deadDataConn := createDeadTestConnection("http://localhost:9200", RoleData)
		deadIngestConn := createDeadTestConnection("http://localhost:9201", RoleIngest)

		err = rolePolicy.DiscoveryUpdate([]*Connection{deadDataConn, deadIngestConn}, nil, nil)
		require.NoError(t, err)

		rolePolicy.pool.RLock()
		_, dataAdmitted := rolePolicy.pool.mu.members[deadDataConn]
		_, ingestAdmitted := rolePolicy.pool.mu.members[deadIngestConn]
		deadCount := len(rolePolicy.pool.mu.dead)
		readyCount := len(rolePolicy.pool.mu.ready)
		rolePolicy.pool.RUnlock()

		require.True(t, dataAdmitted, "role-matching dead connection must be admitted to the pool")
		require.False(t, ingestAdmitted, "non-matching dead connection must be dropped")
		require.Equal(t, 1, deadCount, "the matching dead connection belongs on the dead list")
		require.Zero(t, readyCount, "a dead connection must not enter the ready list")
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
		healthCheck := func(ctx context.Context, _ *Connection, u *url.URL) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK}, nil
		}

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
		require.Empty(t, result)
	})

	t.Run("nil roles returns empty string", func(t *testing.T) {
		result, err := NormalizeRoles(nil)
		require.NoError(t, err)
		require.Empty(t, result)
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
		require.Empty(t, result)
		var invalidRoleErr InvalidRoleError
		require.ErrorAs(t, err, &invalidRoleErr)
	})

	t.Run("first role valid, second invalid", func(t *testing.T) {
		result, err := NormalizeRoles([]string{"data", "bad,role"})
		require.Error(t, err)
		require.Empty(t, result)
		var invalidRoleErr InvalidRoleError
		require.ErrorAs(t, err, &invalidRoleErr)
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

// TestRolePolicyDiscoveryUpdateConcurrent guards against the data race that
// occurs when two DiscoverNodes calls drive DiscoveryUpdate on a shared pool
// simultaneously. recalculateWarmupParamsWithLock writes the pool's warmupRounds,
// warmupSkipCount, and activeListCap fields; those writes must happen under the
// pool write lock (as the roundrobin and cluster_coordinator policies already
// do). Without the lock, concurrent updates race on those fields. Run under
// `go test -race` to detect a regression.
func TestRolePolicyDiscoveryUpdateConcurrent(t *testing.T) {
	policy, err := NewRolePolicy(RoleData)
	require.NoError(t, err)

	rolePolicy := policy.(*RolePolicy)
	require.NoError(t, rolePolicy.configurePolicySettings(createTestConfig()))

	// Two connections that alternate in and out of the pool so each goroutine
	// drives a real add/remove pass through recalculateWarmupParamsWithLock.
	connA := createTestConnection("http://localhost:9200", RoleData)
	connB := createTestConnection("http://localhost:9201", RoleData)

	const goroutines = 8
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		conn := connA
		if i%2 == 1 {
			conn = connB
		}
		go func() {
			defer wg.Done()
			for range iterations {
				_ = rolePolicy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
				_ = rolePolicy.DiscoveryUpdate(nil, []*Connection{conn}, nil)
			}
		}()
	}
	wg.Wait()
}

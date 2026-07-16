// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// newUnverifiedDiscoveredConn builds a connection in the exact state a freshly
// discovered node has before any successful health check:
// lcDead | lcNeedsWarmup | lcNeedsHardware (see createConnection in discovery.go).
//
// lcNeedsHardware is the bit that marks "never proven reachable": a node whose
// publish_address may be unroutable (a NAT'd or misconfigured cluster, as in
// CI). A connection still carrying it has never served a request. It must NOT
// count toward a policy being "enabled", or it masks the seed-URL fallback.
func newUnverifiedDiscoveredConn(urlStr string, roles ...string) *Connection {
	u, _ := url.Parse(urlStr)
	conn := &Connection{
		URL:       u,
		URLString: u.String(),
		Roles:     make(roleSet),
	}
	for _, role := range roles {
		conn.Roles[role] = struct{}{}
	}
	conn.setLifecycleBit(lcDead | lcNeedsWarmup | lcNeedsHardware)
	conn.mu.Lock()
	conn.markAsDeadWithLock()
	conn.mu.Unlock()
	return conn
}

// TestPolicyEnabledExcludesUnverifiedDiscovered pins the seed-fallback bug at
// the policy layer.
//
// Every routing policy caches an "enabled" bit during DiscoveryUpdate. The bug:
// that bit was computed as len(ready) > 0 || len(dead) > 0, which counts a
// dead-but-never-verified discovered connection as available. So the instant
// discovery returns an unroutable node, the policy reports enabled, Route hands
// it the request stream, and it is served as a zombie -- instead of the request
// cascading to the user-supplied seed-URL fallback.
//
// Desired behavior: a policy whose ONLY connection is a never-verified
// discovered node reports IsEnabled() == false, so Route returns
// ErrNoConnections and the seed fallback takes over until a discovered node
// actually proves reachable.
func TestPolicyEnabledExcludesUnverifiedDiscovered(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T) (Policy, *Connection)
	}{
		{
			name: "RoundRobin",
			build: func(t *testing.T) (Policy, *Connection) {
				t.Helper()
				p := NewRoundRobinPolicy().(*RoundRobinPolicy)
				require.NoError(t, p.configurePolicySettings(createTestConfig()))
				return p, newUnverifiedDiscoveredConn("http://node1:9200")
			},
		},
		{
			name: "Coordinator",
			build: func(t *testing.T) (Policy, *Connection) {
				t.Helper()
				p := NewCoordinatorPolicy().(*CoordinatorPolicy)
				require.NoError(t, p.configurePolicySettings(createTestConfig()))
				// A coordinating-only node has an empty role set.
				return p, newUnverifiedDiscoveredConn("http://node1:9200")
			},
		},
		{
			name: "Role",
			build: func(t *testing.T) (Policy, *Connection) {
				t.Helper()
				pol, err := NewRolePolicy(RoleData)
				require.NoError(t, err)
				p := pol.(*RolePolicy)
				require.NoError(t, p.configurePolicySettings(createTestConfig()))
				return p, newUnverifiedDiscoveredConn("http://node1:9200", RoleData)
			},
		},
		{
			name: "IndexRouter",
			build: func(t *testing.T) (Policy, *Connection) {
				t.Helper()
				p := newIndexRouter(indexSlotCacheConfig{})
				return p, newUnverifiedDiscoveredConn("http://node1:9200", RoleData)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, conn := tt.build(t)
			require.NoError(t, p.DiscoveryUpdate([]*Connection{conn}, nil, nil))
			require.False(t, p.IsEnabled(),
				"policy must report NOT enabled when its only connection is a "+
					"never-verified discovered node; counting it as available "+
					"masks the seed-URL fallback (zombie routing)")
		})
	}
}

// TestPolicyEnabledIncludesVerifiedDiscovered is the positive control: once a
// discovered connection is confirmed reachable (lcActive, lcNeedsHardware
// cleared), the policy MUST report enabled. Guards the fix against
// over-correcting into "always disabled".
func TestPolicyEnabledIncludesVerifiedDiscovered(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T) (Policy, *Connection)
	}{
		{
			name: "RoundRobin",
			build: func(t *testing.T) (Policy, *Connection) {
				t.Helper()
				p := NewRoundRobinPolicy().(*RoundRobinPolicy)
				require.NoError(t, p.configurePolicySettings(createTestConfig()))
				return p, createTestConnection("http://node1:9200")
			},
		},
		{
			name: "Coordinator",
			build: func(t *testing.T) (Policy, *Connection) {
				t.Helper()
				p := NewCoordinatorPolicy().(*CoordinatorPolicy)
				require.NoError(t, p.configurePolicySettings(createTestConfig()))
				return p, createTestConnection("http://node1:9200")
			},
		},
		{
			name: "Role",
			build: func(t *testing.T) (Policy, *Connection) {
				t.Helper()
				pol, err := NewRolePolicy(RoleData)
				require.NoError(t, err)
				p := pol.(*RolePolicy)
				require.NoError(t, p.configurePolicySettings(createTestConfig()))
				return p, createTestConnection("http://node1:9200", RoleData)
			},
		},
		{
			name: "IndexRouter",
			build: func(t *testing.T) (Policy, *Connection) {
				t.Helper()
				p := newIndexRouter(indexSlotCacheConfig{})
				return p, createTestConnection("http://node1:9200", RoleData)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, conn := tt.build(t)
			require.NoError(t, p.DiscoveryUpdate([]*Connection{conn}, nil, nil))
			require.True(t, p.IsEnabled(),
				"policy must report enabled when it has a verified, reachable connection")
		})
	}
}

// TestRolePolicyEnabledRecomputedOnUnchangedCycle guards the regression where
// RolePolicy only recomputed its enabled bit inside the add/remove helpers, so
// on an unchanged-only discovery cycle (the steady state) the bit went stale.
//
// Sequence: a discovered node is added while unverified (dead + lcNeedsHardware)
// -> policy not enabled. A health check then verifies it off the discovery path
// (lcNeedsHardware cleared, promoted to ready) without any membership change.
// The next discovery cycle carries the node only in `unchanged`. RolePolicy must
// still recompute and report enabled; otherwise Route skips it forever and
// traffic pins to the seed fallback even though a healthy node exists.
func TestRolePolicyEnabledRecomputedOnUnchangedCycle(t *testing.T) {
	pol, err := NewRolePolicy(RoleData)
	require.NoError(t, err)
	p := pol.(*RolePolicy)
	require.NoError(t, p.configurePolicySettings(createTestConfig()))

	conn := newUnverifiedDiscoveredConn("http://node1:9200", RoleData)

	// Cycle 1: node joins unverified -> not routable, policy disabled.
	require.NoError(t, p.DiscoveryUpdate([]*Connection{conn}, nil, nil))
	require.False(t, p.IsEnabled(), "unverified discovered node must not enable the policy")

	// Health check verifies the node off the discovery path: clear
	// lcNeedsHardware and promote it to ready, mirroring resurrectWithLock.
	conn.mu.Lock()
	require.NoError(t, conn.casLifecycle(conn.loadConnState(), 0, 0, lcNeedsHardware))
	conn.markAsHealthyWithLock()
	conn.mu.Unlock()
	p.pool.Lock()
	p.pool.resurrectWithLock(conn)
	p.pool.Unlock()

	// Cycle 2: unchanged-only discovery (no add/remove). The bit must refresh.
	require.NoError(t, p.DiscoveryUpdate(nil, nil, []*Connection{conn}))
	require.True(t, p.IsEnabled(),
		"RolePolicy must recompute enabled on an unchanged-only cycle once a node is verified")
}

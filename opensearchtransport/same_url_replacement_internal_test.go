// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

func TestRoutingPoliciesPreserveSameURLReplacement(t *testing.T) {
	t.Parallel()

	policies := []struct {
		name      string
		roles     []string
		newPolicy func() (Policy, error)
	}{
		{name: "round robin", roles: []string{RoleData}, newPolicy: func() (Policy, error) {
			return NewRoundRobinPolicy(), nil
		}},
		{name: "data role", roles: []string{RoleData}, newPolicy: func() (Policy, error) {
			return NewRolePolicy(RoleData)
		}},
		{name: "index router", roles: []string{RoleData}, newPolicy: func() (Policy, error) {
			return NewIndexRouter()
		}},
		{name: "document router", roles: []string{RoleData}, newPolicy: func() (Policy, error) {
			return NewDocRouter()
		}},
		{name: "coordinator", newPolicy: func() (Policy, error) {
			return NewCoordinatorPolicy(), nil
		}},
		{name: "coordinating role", newPolicy: func() (Policy, error) {
			return NewRolePolicy(RoleCoordinatingOnly)
		}},
	}
	tests := []struct {
		name         string
		oldURL       string
		oldID        string
		oldName      string
		rolesChanged bool
		unchanged    bool
	}{
		{name: "seed becomes discovered node"},
		{name: "node ID changes", oldID: "previous", oldName: "node"},
		{name: "node name changes", oldID: "node", oldName: "previous"},
		{name: "node roles change", oldID: "node", oldName: "node", rolesChanged: true},
		{
			name:    "different URL replacement",
			oldURL:  "http://127.0.0.1:9201",
			oldID:   "node",
			oldName: "node",
		},
		{name: "unchanged node", oldID: "node", oldName: "node", unchanged: true},
	}

	for _, policyCase := range policies {
		for _, tt := range tests {
			t.Run(policyCase.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				policy, err := policyCase.newPolicy()
				require.NoError(t, err)

				config := createTestConfig()
				config.ctx = t.Context()
				require.NoError(t, policy.(policyConfigurable).configurePolicySettings(config))

				oldURL := tt.oldURL
				if oldURL == "" {
					oldURL = "http://127.0.0.1:9200"
				}

				oldRoles := policyCase.roles
				if tt.oldID == "" {
					oldRoles = nil
				}
				if tt.rolesChanged {
					oldRoles = append([]string{RoleIngest}, policyCase.roles...)
				}

				old := createTestConnection(oldURL, oldRoles...)
				old.ID = tt.oldID
				old.Name = tt.oldName
				old.URLString = old.URL.String()
				old.seed = tt.oldID == ""
				replacement := createTestConnection("http://127.0.0.1:9200", policyCase.roles...)
				replacement.ID = "node"
				replacement.Name = "node"
				replacement.URLString = replacement.URL.String()
				require.NoError(t, policy.DiscoveryUpdate([]*Connection{old}, nil, nil))

				if tt.unchanged {
					replacement = old
					err = policy.DiscoveryUpdate(nil, nil, []*Connection{old})
				} else {
					err = policy.DiscoveryUpdate([]*Connection{replacement}, []*Connection{old}, nil)
				}
				require.NoError(t, err)
				require.True(t, policy.IsEnabled(), "the replacement must remain routable")

				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/index/_doc/document", nil)
				require.NoError(t, err)

				hop, err := policy.Eval(t.Context(), req)
				require.NoError(t, err)
				require.Same(t, replacement, hop.Conn)
			})
		}
	}
}

func TestRoutingPoliciesPreserveSameURLReplacementOfDeadNode(t *testing.T) {
	t.Parallel()

	policies := []struct {
		name      string
		roles     []string
		newPolicy func() (Policy, error)
	}{
		{name: "round robin", roles: []string{RoleData}, newPolicy: func() (Policy, error) {
			return NewRoundRobinPolicy(), nil
		}},
		{name: "coordinator", newPolicy: func() (Policy, error) {
			return NewCoordinatorPolicy(), nil
		}},
		{name: "data role", roles: []string{RoleData}, newPolicy: func() (Policy, error) {
			return NewRolePolicy(RoleData)
		}},
	}

	for _, tt := range policies {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			policy, err := tt.newPolicy()
			require.NoError(t, err)

			config := createTestConfig()
			config.ctx = t.Context()
			require.NoError(t, policy.(policyConfigurable).configurePolicySettings(config))

			old := createDeadTestConnection("http://127.0.0.1:9200", tt.roles...)
			unrelated := createDeadTestConnection("http://127.0.0.1:9201", tt.roles...)
			replacement := createTestConnection("http://127.0.0.1:9200", tt.roles...)

			for _, conn := range []*Connection{old, unrelated, replacement} {
				conn.URLString = conn.URL.String()
			}

			require.NoError(t, policy.DiscoveryUpdate([]*Connection{old, unrelated}, nil, nil))

			err = policy.DiscoveryUpdate([]*Connection{replacement}, []*Connection{old}, []*Connection{unrelated})
			require.NoError(t, err)
			require.True(t, policy.IsEnabled(), "the replacement must remain routable")

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/index/_doc/document", nil)
			require.NoError(t, err)

			hop, err := policy.Eval(t.Context(), req)
			require.NoError(t, err)
			require.Same(t, replacement, hop.Conn)

			var pool *multiServerPool

			switch policy := policy.(type) {
			case *RoundRobinPolicy:
				pool = policy.pool
			case *CoordinatorPolicy:
				pool = policy.pool
			case *RolePolicy:
				pool = policy.pool
			}

			require.NotNil(t, pool)

			pool.RLock()
			defer pool.RUnlock()

			require.NotContains(t, pool.mu.members, old)
			require.Contains(t, pool.mu.members, replacement)
			require.Len(t, pool.mu.dead, 1)
			require.Same(t, unrelated, pool.mu.dead[0], "removing the old node must preserve other dead nodes")
		})
	}
}

func TestDiscoverySameURLReplacementRoutesAfterHealthCheck(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		router, err := NewDefaultRouter()
		require.NoError(t, err)

		transport := mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(validOpenSearchRootResponse())),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})
		urls := []*url.URL{
			{Scheme: "http", Host: "127.0.0.1:9200"},
			{Scheme: "http", Host: "127.0.0.1:9201"},
		}
		tr, err := New(Config{
			URLs:                    urls,
			Context:                 t.Context(),
			Transport:               transport,
			Router:                  router,
			DiscoverNodesInterval:   -1,
			NodeStatsInterval:       -1,
			SkipConnectionShuffle:   true,
			ResurrectTimeoutInitial: time.Millisecond,
			ResurrectTimeoutMax:     time.Millisecond,
			MinimumResurrectTimeout: time.Millisecond,
		})
		require.NoError(t, err)
		defer func() {
			require.NoError(t, tr.Close())
		}()

		synctest.Wait()

		nodes := []nodeInfo{
			{ID: "data", Name: "data", Roles: []string{RoleData}, url: urls[0]},
			{ID: "manager", Name: "manager", Roles: []string{RoleClusterManager}, url: urls[1]},
		}
		previous := tr.findConnectionByURL(tr.mu.connectionPool, urls[0].String())

		for _, change := range []string{"seed", "ID", "name", "roles"} {
			switch change {
			case "ID":
				nodes[0].ID = "replacement"
			case "name":
				nodes[0].Name = "replacement"
			case "roles":
				nodes[0].Roles = []string{RoleData, RoleIngest}
			}

			require.NoError(t, tr.nodeDiscovery(t.Context(), nodes))
			replacement := tr.findConnectionByURL(tr.mu.connectionPool, urls[0].String())
			require.NotSame(t, previous, replacement, change)

			time.Sleep(2 * time.Millisecond)
			synctest.Wait()
			require.True(t, replacement.loadConnState().lifecycle().has(lcActive), change)

			require.NoError(t, tr.nodeDiscovery(t.Context(), nodes))
			require.Same(t, replacement, tr.findConnectionByURL(tr.mu.connectionPool, urls[0].String()))
			require.NoError(t, tr.router.CheckDead(t.Context(), tr.DefaultHealthCheck))

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/index/_search", nil)
			require.NoError(t, err)

			hop, err := tr.router.Route(t.Context(), req)
			require.NoError(t, err, change)
			require.Same(t, replacement, hop.Conn, change)

			previous = replacement
		}
	})
}

func TestRoutingPolicyDiscoveryUpdateSkips(t *testing.T) {
	t.Parallel()

	poolConns := func(pool *multiServerPool) func() []*Connection {
		return func() []*Connection {
			pool.RLock()
			defer pool.RUnlock()
			return slices.Concat(pool.mu.ready, pool.mu.dead)
		}
	}

	// Each constructor returns the policy, its state bits, and a func listing
	// the connections the policy holds.
	policies := []struct {
		name      string
		roles     []string
		newPolicy func(t *testing.T) (Policy, *atomic.Int32, func() []*Connection)
	}{
		{name: "round robin", roles: []string{RoleData}, newPolicy: func(t *testing.T) (Policy, *atomic.Int32, func() []*Connection) {
			t.Helper()
			p, ok := NewRoundRobinPolicy().(*RoundRobinPolicy)
			require.True(t, ok)
			return p, &p.policyState, func() []*Connection { return poolConns(p.pool)() }
		}},
		{name: "data role", roles: []string{RoleData}, newPolicy: func(t *testing.T) (Policy, *atomic.Int32, func() []*Connection) {
			t.Helper()
			policy, err := NewRolePolicy(RoleData)
			require.NoError(t, err)
			p, ok := policy.(*RolePolicy)
			require.True(t, ok)
			return p, &p.policyState, func() []*Connection { return poolConns(p.pool)() }
		}},
		{name: "index router", roles: []string{RoleData}, newPolicy: func(t *testing.T) (Policy, *atomic.Int32, func() []*Connection) {
			t.Helper()
			p, err := NewIndexRouter()
			require.NoError(t, err)
			return p, &p.policyState, func() []*Connection {
				p.mu.RLock()
				defer p.mu.RUnlock()
				return slices.Clone(p.mu.activeConns)
			}
		}},
		{name: "document router", roles: []string{RoleData}, newPolicy: func(t *testing.T) (Policy, *atomic.Int32, func() []*Connection) {
			t.Helper()
			p, err := NewDocRouter()
			require.NoError(t, err)
			return p, &p.policyState, func() []*Connection {
				p.mu.RLock()
				defer p.mu.RUnlock()
				return slices.Clone(p.mu.activeConns)
			}
		}},
		{name: "coordinator", newPolicy: func(t *testing.T) (Policy, *atomic.Int32, func() []*Connection) {
			t.Helper()
			p, ok := NewCoordinatorPolicy().(*CoordinatorPolicy)
			require.True(t, ok)
			return p, &p.policyState, func() []*Connection { return poolConns(p.pool)() }
		}},
		{name: "coordinating role", newPolicy: func(t *testing.T) (Policy, *atomic.Int32, func() []*Connection) {
			t.Helper()
			policy, err := NewRolePolicy(RoleCoordinatingOnly)
			require.NoError(t, err)
			p, ok := policy.(*RolePolicy)
			require.True(t, ok)
			return p, &p.policyState, func() []*Connection { return poolConns(p.pool)() }
		}},
	}

	// Each case starts from a policy holding only seeded; after the update the
	// policy must still hold exactly seeded and route to it.
	tests := []struct {
		name   string
		update func(t *testing.T, policy Policy, state *atomic.Int32, seeded, other *Connection)
	}{
		{
			name: "no added or removed connections",
			update: func(t *testing.T, policy Policy, _ *atomic.Int32, seeded, _ *Connection) {
				t.Helper()
				require.NoError(t, policy.DiscoveryUpdate(nil, nil, []*Connection{seeded}))
			},
		},
		{
			name: "connection added and removed in one update",
			update: func(t *testing.T, policy Policy, _ *atomic.Int32, _, other *Connection) {
				t.Helper()
				require.NoError(t, policy.DiscoveryUpdate([]*Connection{other}, []*Connection{other}, nil))
			},
		},
		{
			name: "connection added twice",
			update: func(t *testing.T, policy Policy, _ *atomic.Int32, seeded, _ *Connection) {
				t.Helper()
				require.NoError(t, policy.DiscoveryUpdate([]*Connection{seeded}, nil, nil))
			},
		},
		{
			name: "update while disabled by environment",
			update: func(t *testing.T, policy Policy, state *atomic.Int32, seeded, _ *Connection) {
				t.Helper()
				psSetEnvOverride(state, false)
				require.NoError(t, policy.DiscoveryUpdate(nil, []*Connection{seeded}, nil))
				require.False(t, policy.IsEnabled())
				state.Store(state.Load() &^ psEnvMask)
			},
		},
	}

	for _, policyCase := range policies {
		for _, tt := range tests {
			t.Run(policyCase.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				policy, state, conns := policyCase.newPolicy(t)
				config := createTestConfig()
				config.ctx = t.Context()
				require.NoError(t, policy.(policyConfigurable).configurePolicySettings(config))

				seeded := createTestConnection("http://127.0.0.1:9200", policyCase.roles...)
				seeded.URLString = seeded.URL.String()
				other := createTestConnection("http://127.0.0.1:9201", policyCase.roles...)
				other.URLString = other.URL.String()
				require.NoError(t, policy.DiscoveryUpdate([]*Connection{seeded}, nil, nil))

				tt.update(t, policy, state, seeded, other)
				require.True(t, policy.IsEnabled())

				held := conns()
				require.Len(t, held, 1, "the policy must hold only the seeded connection")
				require.Same(t, seeded, held[0])

				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/index/_doc/document", nil)
				require.NoError(t, err)

				hop, err := policy.Eval(t.Context(), req)
				require.NoError(t, err)
				require.Same(t, seeded, hop.Conn)
			})
		}
	}
}

func TestPoolPolicyDiscoveryUpdateBeforeConfigure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		policy Policy
		roles  []string
	}{
		{name: "round robin", policy: NewRoundRobinPolicy(), roles: []string{RoleData}},
		{name: "coordinator", policy: NewCoordinatorPolicy()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn := createTestConnection("http://127.0.0.1:9200", tt.roles...)
			require.NoError(t, tt.policy.DiscoveryUpdate([]*Connection{conn}, nil, nil))
			require.False(t, tt.policy.IsEnabled(), "a policy without a pool must ignore discovery")
		})
	}
}

func TestDiscoveryReplacesDuplicateSeedURLs(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		router, err := NewDefaultRouter()
		require.NoError(t, err)

		transport := mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(validOpenSearchRootResponse())),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})
		data := &url.URL{Scheme: "http", Host: "127.0.0.1:9200"}
		manager := &url.URL{Scheme: "http", Host: "127.0.0.1:9201"}
		tr, err := New(Config{
			URLs:                  []*url.URL{data, data, manager},
			Context:               t.Context(),
			Transport:             transport,
			Router:                router,
			DiscoverNodesInterval: -1,
			NodeStatsInterval:     -1,
			SkipConnectionShuffle: true,
		})
		require.NoError(t, err)
		defer func() {
			require.NoError(t, tr.Close())
		}()

		synctest.Wait()

		// A repeated seed URL still counts as its own seed until discovery runs.
		tr.mu.RLock()
		pool, isMulti := tr.mu.connectionPool.(*multiServerPool)
		tr.mu.RUnlock()
		require.True(t, isMulti, "a repeated seed URL must still build a multi-server pool")
		require.Len(t, pool.connections(), 3)

		require.NoError(t, tr.nodeDiscovery(t.Context(), []nodeInfo{
			{ID: "data", Name: "data", Roles: []string{RoleData}, url: data},
			{ID: "manager", Name: "manager", Roles: []string{RoleClusterManager}, url: manager},
		}))

		root, ok := tr.router.(Policy)
		require.True(t, ok)
		held := policyConnections(root)
		require.NotEmpty(t, held)
		for _, conn := range held {
			require.False(t, conn.seed, "discovery must replace every seed, including a repeated seed URL: %q", conn.URLString)
		}
	})
}

// policyConnections returns every connection held by p and the policies under it.
func policyConnections(p Policy) []*Connection {
	pool := func(pool *multiServerPool) []*Connection {
		if pool == nil {
			return nil
		}
		pool.RLock()
		defer pool.RUnlock()
		return slices.Concat(pool.mu.ready, pool.mu.dead)
	}

	var conns []*Connection
	switch p := p.(type) {
	case *RoundRobinPolicy:
		conns = pool(p.pool)
	case *RolePolicy:
		conns = pool(p.pool)
	case *CoordinatorPolicy:
		conns = pool(p.pool)
	case *IndexRouter:
		p.mu.RLock()
		conns = slices.Clone(p.mu.activeConns)
		p.mu.RUnlock()
	case *DocRouter:
		p.mu.RLock()
		conns = slices.Clone(p.mu.activeConns)
		p.mu.RUnlock()
	}

	if walker, ok := p.(policyTreeWalker); ok {
		for _, child := range walker.childPolicies() {
			conns = append(conns, policyConnections(child)...)
		}
	}

	return conns
}

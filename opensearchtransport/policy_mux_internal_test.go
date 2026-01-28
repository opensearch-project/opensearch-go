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

func TestRouteMux(t *testing.T) {
	t.Run("NewRouteMux creates route", func(t *testing.T) {
		route, err := NewRouteMux("POST /_bulk", NewNullPolicy())
		require.NoError(t, err)
		require.NotNil(t, route)
		require.IsType(t, &RouteMux{}, route)
	})

	t.Run("NewRouteMux with nil policy returns error", func(t *testing.T) {
		route, err := NewRouteMux("POST /_bulk", nil)
		require.Error(t, err)
		require.Nil(t, route)
		require.Contains(t, err.Error(), "policy cannot be nil")
	})

	t.Run("NewRouteMux with invalid pattern returns error", func(t *testing.T) {
		route, err := NewRouteMux("/_bulk", NewNullPolicy())
		require.Error(t, err)
		require.Nil(t, route)
	})

	t.Run("NewRouteMux with invalid HTTP method returns error", func(t *testing.T) {
		route, err := NewRouteMux("INVALID /_bulk", NewNullPolicy())
		require.Error(t, err)
		require.Nil(t, route)
	})

	t.Run("NewRouteMux with empty pattern returns error", func(t *testing.T) {
		route, err := NewRouteMux("", NewNullPolicy())
		require.Error(t, err)
		require.Nil(t, route)
	})

	t.Run("Policy returns underlying policy", func(t *testing.T) {
		policy := NewNullPolicy()
		route, err := NewRouteMux("POST /_bulk", policy)
		require.NoError(t, err)

		require.Same(t, policy, route.Policy())
	})

	t.Run("mustNewRouteMux succeeds with valid pattern", func(t *testing.T) {
		require.NotPanics(t, func() {
			route := mustNewRouteMux("POST /_bulk", NewNullPolicy())
			require.NotNil(t, route)
		})
	})

	t.Run("mustNewRouteMux panics with nil policy", func(t *testing.T) {
		require.Panics(t, func() {
			mustNewRouteMux("POST /_bulk", nil)
		})
	})

	t.Run("mustNewRouteMux panics with invalid pattern", func(t *testing.T) {
		require.Panics(t, func() {
			mustNewRouteMux("/_bulk", NewNullPolicy())
		})
	})
}

func TestMuxPolicy(t *testing.T) {
	t.Run("NewMuxPolicy creates policy", func(t *testing.T) {
		routes := []Route{
			mustNewRouteMux("POST /_bulk", NewNullPolicy()),
		}
		policy := NewMuxPolicy(routes)
		require.NotNil(t, policy)
		require.IsType(t, &MuxPolicy{}, policy)
	})

	t.Run("NewMuxPolicy with empty routes", func(t *testing.T) {
		policy := NewMuxPolicy([]Route{})
		require.NotNil(t, policy)
	})

	t.Run("IsEnabled returns false initially", func(t *testing.T) {
		routes := []Route{
			mustNewRouteMux("POST /_bulk", NewRoundRobinPolicy()),
		}
		policy := NewMuxPolicy(routes)
		require.False(t, policy.IsEnabled())
	})

	t.Run("IsEnabled returns true after DiscoveryUpdate with enabled policy", func(t *testing.T) {
		dataPolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		dataPolicy.configurePolicySettings(createTestConfig())

		routes := []Route{
			mustNewRouteMux("POST /_bulk", dataPolicy),
		}
		policy := NewMuxPolicy(routes).(*MuxPolicy)
		policy.configurePolicySettings(createTestConfig())

		conn := createTestConnection("http://localhost:9200", RoleData)
		err := policy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)

		// Get connection via Next and promote to live pool
		zombieConn, err := dataPolicy.pool.Next()
		require.NoError(t, err)
		require.NotNil(t, zombieConn)
		dataPolicy.pool.OnSuccess(zombieConn)

		// Refresh MuxPolicy's cached isEnabled state
		err = policy.DiscoveryUpdate(nil, nil, nil)
		require.NoError(t, err)

		require.True(t, policy.IsEnabled())
	})

	t.Run("Eval routes system endpoints to systemMux", func(t *testing.T) {
		dataPolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		dataPolicy.configurePolicySettings(createTestConfig())

		routes := []Route{
			mustNewRouteMux("POST /_bulk", dataPolicy),
		}
		policy := NewMuxPolicy(routes)

		ctx := context.Background()
		req, _ := http.NewRequest(http.MethodPost, "/_bulk", nil)

		pool, err := policy.Eval(ctx, req)
		require.NotNil(t, pool)
		require.NoError(t, err)
	})

	t.Run("Eval routes index endpoints to indexMux", func(t *testing.T) {
		dataPolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		dataPolicy.configurePolicySettings(createTestConfig())

		routes := []Route{
			mustNewRouteMux("POST /myindex/_search", dataPolicy),
		}
		policy := NewMuxPolicy(routes)

		ctx := context.Background()
		req, _ := http.NewRequest(http.MethodPost, "/myindex/_search", nil)

		pool, err := policy.Eval(ctx, req)
		require.NotNil(t, pool)
		require.NoError(t, err)
	})

	t.Run("Eval returns nil when no route matches", func(t *testing.T) {
		routes := []Route{
			mustNewRouteMux("POST /_bulk", NewNullPolicy()),
		}
		policy := NewMuxPolicy(routes)

		ctx := context.Background()
		req, _ := http.NewRequest(http.MethodGet, "/_search", nil)

		pool, err := policy.Eval(ctx, req)
		require.Nil(t, pool)
		require.NoError(t, err)
	})

	t.Run("Eval with nil systemMux", func(t *testing.T) {
		// Create policy with only index routes
		routes := []Route{
			mustNewRouteMux("POST /index/_search", NewNullPolicy()),
		}
		policy := NewMuxPolicy(routes)

		ctx := context.Background()
		req, _ := http.NewRequest(http.MethodPost, "/_bulk", nil)

		pool, err := policy.Eval(ctx, req)
		require.Nil(t, pool)
		require.NoError(t, err)
	})

	t.Run("Eval with nil indexMux", func(t *testing.T) {
		// Create policy with only system routes
		routes := []Route{
			mustNewRouteMux("POST /_bulk", NewNullPolicy()),
		}
		policy := NewMuxPolicy(routes)

		ctx := context.Background()
		req, _ := http.NewRequest(http.MethodPost, "/index/_search", nil)

		pool, err := policy.Eval(ctx, req)
		require.Nil(t, pool)
		require.NoError(t, err)
	})

	t.Run("Eval reuses policies", func(t *testing.T) {
		sharedPolicy := NewNullPolicy()
		routes := []Route{
			mustNewRouteMux("POST /_bulk", sharedPolicy),
			mustNewRouteMux("POST /_search", sharedPolicy),
		}
		policy := NewMuxPolicy(routes).(*MuxPolicy)

		// Should have only one unique policy
		require.Len(t, policy.uniquePolicies, 1)
	})

	t.Run("DiscoveryUpdate updates all unique policies", func(t *testing.T) {
		policy1 := NewRoundRobinPolicy().(*RoundRobinPolicy)
		policy1.configurePolicySettings(createTestConfig())

		policy2 := NewRoundRobinPolicy().(*RoundRobinPolicy)
		policy2.configurePolicySettings(createTestConfig())

		routes := []Route{
			mustNewRouteMux("POST /_bulk", policy1),
			mustNewRouteMux("GET /_search", policy2),
		}
		muxPolicy := NewMuxPolicy(routes).(*MuxPolicy)
		muxPolicy.configurePolicySettings(createTestConfig())

		conn := createTestConnection("http://localhost:9200")
		err := muxPolicy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)

		// Both policies should have the connection
		require.Len(t, policy1.pool.connections(), 1)
		require.Len(t, policy2.pool.connections(), 1)
	})

	t.Run("DiscoveryUpdate with shared policy updates once", func(t *testing.T) {
		sharedPolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		sharedPolicy.configurePolicySettings(createTestConfig())

		routes := []Route{
			mustNewRouteMux("POST /_bulk", sharedPolicy),
			mustNewRouteMux("POST /_search", sharedPolicy),
		}
		muxPolicy := NewMuxPolicy(routes).(*MuxPolicy)
		muxPolicy.configurePolicySettings(createTestConfig())

		conn := createTestConnection("http://localhost:9200")
		err := muxPolicy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)

		// Policy should have connection added once
		require.Len(t, sharedPolicy.pool.connections(), 1)
	})

	t.Run("CheckDead delegates to all unique policies", func(t *testing.T) {
		policy1Called := false
		policy2Called := false

		policy1 := &testPolicy{
			checkDeadFunc: func(ctx context.Context, hc HealthCheckFunc) error {
				policy1Called = true
				return nil
			},
		}
		policy2 := &testPolicy{
			checkDeadFunc: func(ctx context.Context, hc HealthCheckFunc) error {
				policy2Called = true
				return nil
			},
		}

		routes := []Route{
			mustNewRouteMux("POST /_bulk", policy1),
			mustNewRouteMux("GET /_search", policy2),
		}
		muxPolicy := NewMuxPolicy(routes)

		ctx := context.Background()
		healthCheck := func(ctx context.Context, u *url.URL) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK}, nil
		}

		err := muxPolicy.CheckDead(ctx, healthCheck)
		require.NoError(t, err)
		require.True(t, policy1Called)
		require.True(t, policy2Called)
	})

	t.Run("configurePolicySettings configures all unique policies", func(t *testing.T) {
		policy1 := NewRoundRobinPolicy().(*RoundRobinPolicy)
		policy2 := NewRoundRobinPolicy().(*RoundRobinPolicy)

		routes := []Route{
			mustNewRouteMux("POST /_bulk", policy1),
			mustNewRouteMux("GET /_search", policy2),
		}
		muxPolicy := NewMuxPolicy(routes).(*MuxPolicy)

		err := muxPolicy.configurePolicySettings(createTestConfig())
		require.NoError(t, err)

		require.NotNil(t, policy1.pool)
		require.NotNil(t, policy2.pool)
	})

	t.Run("configurePolicySettings handles non-configurable policies", func(t *testing.T) {
		routes := []Route{
			mustNewRouteMux("POST /_bulk", NewNullPolicy()),
		}
		muxPolicy := NewMuxPolicy(routes).(*MuxPolicy)

		err := muxPolicy.configurePolicySettings(createTestConfig())
		require.NoError(t, err)
	})

	t.Run("NewMuxPolicy panics with unsupported Route type", func(t *testing.T) {
		require.Panics(t, func() {
			routes := []Route{&unsupportedRoute{}}
			NewMuxPolicy(routes)
		})
	})
}

func TestValidMuxPattern(t *testing.T) {
	t.Run("valid pattern with method and path", func(t *testing.T) {
		path, err := validMuxPattern("POST /_bulk")
		require.NoError(t, err)
		require.Equal(t, "/_bulk", path)
	})

	t.Run("valid pattern with GET", func(t *testing.T) {
		path, err := validMuxPattern("GET /_search")
		require.NoError(t, err)
		require.Equal(t, "/_search", path)
	})

	t.Run("empty pattern returns error", func(t *testing.T) {
		path, err := validMuxPattern("")
		require.Error(t, err)
		require.Empty(t, path)
	})

	t.Run("pattern without method returns error", func(t *testing.T) {
		path, err := validMuxPattern("/_bulk")
		require.Error(t, err)
		require.Empty(t, path)
	})

	t.Run("pattern with invalid method returns error", func(t *testing.T) {
		path, err := validMuxPattern("INVALID /_bulk")
		require.Error(t, err)
		require.Empty(t, path)
	})

	t.Run("pattern with extra fields returns error", func(t *testing.T) {
		path, err := validMuxPattern("POST /_bulk extra")
		require.Error(t, err)
		require.Empty(t, path)
	})

	t.Run("all HTTP methods are valid", func(t *testing.T) {
		methods := []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "CONNECT", "OPTIONS", "TRACE"}
		for _, method := range methods {
			path, err := validMuxPattern(method + " /_test")
			require.NoError(t, err, "Method %s should be valid", method)
			require.Equal(t, "/_test", path)
		}
	})
}

// getResponseWriter gets a policyResponseWriter from the pool and initializes it.
// Caller must call releaseResponseWriter when done.
func getResponseWriter(policy *MuxPolicy) *policyResponseWriter {
	pw := policy.responseWriterPool.Get().(*policyResponseWriter)
	pw.muxPolicy = policy
	return pw
}

// releaseResponseWriter releases the writer back to the pool.
func releaseResponseWriter(policy *MuxPolicy, pw *policyResponseWriter) {
	pw.release()
	policy.responseWriterPool.Put(pw)
}

func TestPolicyResponseWriter(t *testing.T) {
	// Create a minimal MuxPolicy for testing the writer
	policy := NewMuxPolicy([]Route{}).(*MuxPolicy)

	t.Run("Header returns empty header", func(t *testing.T) {
		pw := getResponseWriter(policy)
		defer releaseResponseWriter(policy, pw)

		header := pw.Header()
		require.NotNil(t, header)
		require.Empty(t, header)
	})

	t.Run("Write is no-op", func(t *testing.T) {
		pw := getResponseWriter(policy)
		defer releaseResponseWriter(policy, pw)

		n, err := pw.Write([]byte("test"))
		require.Equal(t, 0, n)
		require.NoError(t, err)
	})

	t.Run("WriteHeader is no-op", func(t *testing.T) {
		pw := getResponseWriter(policy)
		defer releaseResponseWriter(policy, pw)

		require.NotPanics(t, func() {
			pw.WriteHeader(http.StatusOK)
		})
	})
}

// unsupportedRoute is a mock Route type for testing unsupported routes
type unsupportedRoute struct{}

func (r *unsupportedRoute) Policy() Policy {
	return NewNullPolicy()
}

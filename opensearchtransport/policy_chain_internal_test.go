// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPolicyChain(t *testing.T) {
	t.Run("NewPolicy creates policy chain", func(t *testing.T) {
		policy := NewPolicy(NewNullPolicy())
		require.NotNil(t, policy)
		require.IsType(t, &PolicyChain{}, policy)
	})

	t.Run("NewPolicy with multiple policies", func(t *testing.T) {
		policy := NewPolicy(NewNullPolicy(), NewRoundRobinPolicy())
		require.NotNil(t, policy)
		chain := policy.(*PolicyChain)
		require.Len(t, chain.policies, 2)
	})

	t.Run("IsEnabled returns false initially", func(t *testing.T) {
		policy := NewPolicy(NewNullPolicy())
		// NullPolicy is always enabled, but chain starts uninitialized
		require.False(t, policy.IsEnabled())
	})

	t.Run("IsEnabled returns true after DiscoveryUpdate with enabled policy", func(t *testing.T) {
		roundRobin := NewRoundRobinPolicy().(*RoundRobinPolicy)
		roundRobin.configurePolicySettings(createTestConfig())

		policy := NewPolicy(roundRobin)
		conn := createTestConnection("http://localhost:9200")
		err := policy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)

		// Get connection via Next and promote to live pool
		zombieConn, err := roundRobin.pool.Next()
		require.NoError(t, err)
		require.NotNil(t, zombieConn)
		roundRobin.pool.OnSuccess(zombieConn)

		// Refresh PolicyChain's cached isEnabled state
		err = policy.DiscoveryUpdate(nil, nil, nil)
		require.NoError(t, err)

		require.True(t, policy.IsEnabled())
	})

	t.Run("Route returns connection from first matching policy", func(t *testing.T) {
		// Set up first policy with a connection
		firstPolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		firstPolicy.configurePolicySettings(createTestConfig())
		conn1 := createTestConnection("http://localhost:9200")
		firstPolicy.DiscoveryUpdate([]*Connection{conn1}, nil, nil)

		// Get connection via Next and promote to live pool
		zombieConn, _ := firstPolicy.pool.Next()
		firstPolicy.pool.OnSuccess(zombieConn)

		// Second policy
		secondPolicy := NewNullPolicy()

		chain := NewPolicy(firstPolicy, secondPolicy).(*PolicyChain)

		ctx := context.Background()
		req, _ := http.NewRequest(http.MethodGet, "/", nil)

		conn, err := chain.Route(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, conn)
	})

	t.Run("Route skips disabled policies", func(t *testing.T) {
		// First policy disabled (no connections)
		firstPolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		firstPolicy.configurePolicySettings(createTestConfig())

		// Second policy enabled
		secondPolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		secondPolicy.configurePolicySettings(createTestConfig())
		conn := createTestConnection("http://localhost:9200")
		secondPolicy.DiscoveryUpdate([]*Connection{conn}, nil, nil)

		// Get connection via Next and promote to live pool
		zombieConn, _ := secondPolicy.pool.Next()
		secondPolicy.pool.OnSuccess(zombieConn)

		chain := NewPolicy(firstPolicy, secondPolicy).(*PolicyChain)

		ctx := context.Background()
		req, _ := http.NewRequest(http.MethodGet, "/", nil)

		conn, err := chain.Route(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, conn)
	})

	t.Run("Route returns ErrNoConnections when no policies match", func(t *testing.T) {
		policy := NewPolicy(NewNullPolicy())
		chain := policy.(*PolicyChain)

		ctx := context.Background()
		req, _ := http.NewRequest(http.MethodGet, "/", nil)

		conn, err := chain.Route(ctx, req)
		require.ErrorIs(t, err, ErrNoConnections)
		require.Nil(t, conn)
	})

	t.Run("Eval returns pool from first matching policy", func(t *testing.T) {
		firstPolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		firstPolicy.configurePolicySettings(createTestConfig())

		secondPolicy := NewNullPolicy()

		policy := NewPolicy(firstPolicy, secondPolicy)

		ctx := context.Background()
		req, _ := http.NewRequest(http.MethodGet, "/", nil)

		pool, err := policy.Eval(ctx, req)
		require.NotNil(t, pool)
		require.NoError(t, err)
	})

	t.Run("Eval returns nil when all policies return nil", func(t *testing.T) {
		policy := NewPolicy(NewNullPolicy(), NewNullPolicy())

		ctx := context.Background()
		req, _ := http.NewRequest(http.MethodGet, "/", nil)

		pool, err := policy.Eval(ctx, req)
		require.Nil(t, pool)
		require.NoError(t, err)
	})

	t.Run("Eval stops on first error", func(t *testing.T) {
		testErr := errors.New("test error")
		errorPolicy := &testPolicyWithError{err: testErr}

		policy := NewPolicy(errorPolicy, NewNullPolicy())

		ctx := context.Background()
		req, _ := http.NewRequest(http.MethodGet, "/", nil)

		pool, err := policy.Eval(ctx, req)
		require.ErrorIs(t, err, testErr)
		require.Nil(t, pool)
	})

	t.Run("DiscoveryUpdate updates all policies in reverse", func(t *testing.T) {
		firstPolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		firstPolicy.configurePolicySettings(createTestConfig())

		secondPolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		secondPolicy.configurePolicySettings(createTestConfig())

		policy := NewPolicy(firstPolicy, secondPolicy)

		conn := createTestConnection("http://localhost:9200")
		err := policy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)

		// Both policies should have the connection
		require.Len(t, firstPolicy.pool.connections(), 1)
		require.Len(t, secondPolicy.pool.connections(), 1)
	})

	t.Run("DiscoveryUpdate returns first error", func(t *testing.T) {
		testErr := errors.New("test error")
		errorPolicy := &testPolicyWithError{err: testErr}

		policy := NewPolicy(errorPolicy, NewNullPolicy())

		err := policy.DiscoveryUpdate(nil, nil, nil)
		require.ErrorIs(t, err, testErr)
	})

	t.Run("CheckDead delegates to all policies", func(t *testing.T) {
		firstCalled := false
		secondCalled := false

		firstPolicy := &testPolicy{
			checkDeadFunc: func(ctx context.Context, hc HealthCheckFunc) error {
				firstCalled = true
				return nil
			},
		}
		secondPolicy := &testPolicy{
			checkDeadFunc: func(ctx context.Context, hc HealthCheckFunc) error {
				secondCalled = true
				return nil
			},
		}

		policy := NewPolicy(firstPolicy, secondPolicy)

		ctx := context.Background()
		healthCheck := func(ctx context.Context, u *url.URL) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK}, nil
		}

		err := policy.CheckDead(ctx, healthCheck)
		require.NoError(t, err)
		require.True(t, firstCalled)
		require.True(t, secondCalled)
	})

	t.Run("CheckDead returns first error", func(t *testing.T) {
		testErr := errors.New("test error")
		errorPolicy := &testPolicyWithError{err: testErr}

		policy := NewPolicy(errorPolicy, NewNullPolicy())

		ctx := context.Background()
		healthCheck := func(ctx context.Context, u *url.URL) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK}, nil
		}

		err := policy.CheckDead(ctx, healthCheck)
		require.ErrorIs(t, err, testErr)
	})

	t.Run("configurePolicySettings configures all sub-policies", func(t *testing.T) {
		firstPolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		secondPolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)

		chain := NewPolicy(firstPolicy, secondPolicy).(*PolicyChain)

		err := chain.configurePolicySettings(createTestConfig())
		require.NoError(t, err)

		require.NotNil(t, firstPolicy.pool)
		require.NotNil(t, secondPolicy.pool)
	})

	t.Run("configurePolicySettings handles non-configurable policies", func(t *testing.T) {
		policy := NewPolicy(NewNullPolicy(), NewNullPolicy()).(*PolicyChain)

		err := policy.configurePolicySettings(createTestConfig())
		require.NoError(t, err)
	})
}

// testPolicyWithError is a mock policy that returns errors for testing
type testPolicyWithError struct {
	err error
}

func (p *testPolicyWithError) DiscoveryUpdate(added, removed, unchanged []*Connection) error {
	return p.err
}

func (p *testPolicyWithError) CheckDead(ctx context.Context, healthCheck HealthCheckFunc) error {
	return p.err
}

func (p *testPolicyWithError) IsEnabled() bool {
	return true
}

func (p *testPolicyWithError) Eval(ctx context.Context, req *http.Request) (ConnectionPool, error) {
	return nil, p.err
}

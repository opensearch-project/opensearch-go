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

func TestIfEnabledPolicy(t *testing.T) {
	alwaysTrue := func(ctx context.Context, req *http.Request) bool { return true }
	alwaysFalse := func(ctx context.Context, req *http.Request) bool { return false }

	t.Run("NewIfEnabledPolicy creates policy", func(t *testing.T) {
		policy := NewIfEnabledPolicy(alwaysTrue, NewNullPolicy(), NewNullPolicy())
		require.NotNil(t, policy)
		require.IsType(t, &IfEnabledPolicy{}, policy)
	})

	t.Run("IsEnabled returns true when all components valid", func(t *testing.T) {
		policy := NewIfEnabledPolicy(alwaysTrue, NewNullPolicy(), NewNullPolicy())
		require.True(t, policy.IsEnabled())
	})

	t.Run("IsEnabled returns false when condition is nil", func(t *testing.T) {
		policy := NewIfEnabledPolicy(nil, NewNullPolicy(), NewNullPolicy())
		require.False(t, policy.IsEnabled())
	})

	t.Run("IsEnabled returns false when truePolicy is nil", func(t *testing.T) {
		policy := NewIfEnabledPolicy(alwaysTrue, nil, NewNullPolicy())
		require.False(t, policy.IsEnabled())
	})

	t.Run("IsEnabled returns false when falsePolicy is nil", func(t *testing.T) {
		policy := NewIfEnabledPolicy(alwaysTrue, NewNullPolicy(), nil)
		require.False(t, policy.IsEnabled())
	})

	t.Run("Eval uses truePolicy when condition is true", func(t *testing.T) {
		truePolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		truePolicy.configurePolicySettings(createTestConfig())

		falsePolicy := NewNullPolicy()

		policy := NewIfEnabledPolicy(alwaysTrue, truePolicy, falsePolicy)

		ctx := context.Background()
		req, _ := http.NewRequest(http.MethodGet, "/", nil)

		pool, err := policy.Eval(ctx, req)
		require.NotNil(t, pool)
		require.NoError(t, err)
	})

	t.Run("Eval uses falsePolicy when condition is false", func(t *testing.T) {
		truePolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		truePolicy.configurePolicySettings(createTestConfig())

		falsePolicy := NewNullPolicy()

		policy := NewIfEnabledPolicy(alwaysFalse, truePolicy, falsePolicy)

		ctx := context.Background()
		req, _ := http.NewRequest(http.MethodGet, "/", nil)

		pool, err := policy.Eval(ctx, req)
		require.Nil(t, pool)
		require.NoError(t, err)
	})

	t.Run("Eval evaluates condition at runtime", func(t *testing.T) {
		callCount := 0
		condition := func(ctx context.Context, req *http.Request) bool {
			callCount++
			return callCount%2 == 0
		}

		truePolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		truePolicy.configurePolicySettings(createTestConfig())
		falsePolicy := NewNullPolicy()

		policy := NewIfEnabledPolicy(condition, truePolicy, falsePolicy)

		ctx := context.Background()
		req, _ := http.NewRequest(http.MethodGet, "/", nil)

		// First call: condition returns false (callCount=1)
		pool1, err1 := policy.Eval(ctx, req)
		require.Nil(t, pool1)
		require.NoError(t, err1)

		// Second call: condition returns true (callCount=2)
		pool2, err2 := policy.Eval(ctx, req)
		require.NotNil(t, pool2)
		require.NoError(t, err2)
	})

	t.Run("DiscoveryUpdate updates both sub-policies", func(t *testing.T) {
		truePolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		truePolicy.configurePolicySettings(createTestConfig())

		falsePolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		falsePolicy.configurePolicySettings(createTestConfig())

		policy := NewIfEnabledPolicy(alwaysTrue, truePolicy, falsePolicy)

		conn := createTestConnection("http://localhost:9200")
		err := policy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)

		// Both policies should have the connection
		require.Len(t, truePolicy.pool.connections(), 1)
		require.Len(t, falsePolicy.pool.connections(), 1)
	})

	t.Run("DiscoveryUpdate returns first error", func(t *testing.T) {
		policy := NewIfEnabledPolicy(alwaysTrue, NewNullPolicy(), NewNullPolicy())

		// Null policies don't error, so this just verifies no panic
		err := policy.DiscoveryUpdate(nil, nil, nil)
		require.NoError(t, err)
	})

	t.Run("CheckDead delegates to both sub-policies", func(t *testing.T) {
		trueCalled := false
		falseCalled := false

		truePolicy := &testPolicy{
			checkDeadFunc: func(ctx context.Context, hc HealthCheckFunc) error {
				trueCalled = true
				return nil
			},
		}
		falsePolicy := &testPolicy{
			checkDeadFunc: func(ctx context.Context, hc HealthCheckFunc) error {
				falseCalled = true
				return nil
			},
		}

		policy := NewIfEnabledPolicy(alwaysTrue, truePolicy, falsePolicy)

		ctx := context.Background()
		healthCheck := func(ctx context.Context, u *url.URL) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK}, nil
		}

		err := policy.CheckDead(ctx, healthCheck)
		require.NoError(t, err)
		require.True(t, trueCalled)
		require.True(t, falseCalled)
	})

	t.Run("configurePolicySettings configures both sub-policies", func(t *testing.T) {
		truePolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)
		falsePolicy := NewRoundRobinPolicy().(*RoundRobinPolicy)

		policy := NewIfEnabledPolicy(alwaysTrue, truePolicy, falsePolicy).(*IfEnabledPolicy)

		err := policy.configurePolicySettings(createTestConfig())
		require.NoError(t, err)

		require.NotNil(t, truePolicy.pool)
		require.NotNil(t, falsePolicy.pool)
	})

	t.Run("configurePolicySettings handles non-configurable policies", func(t *testing.T) {
		truePolicy := NewNullPolicy()
		falsePolicy := NewNullPolicy()

		policy := NewIfEnabledPolicy(alwaysTrue, truePolicy, falsePolicy).(*IfEnabledPolicy)

		err := policy.configurePolicySettings(createTestConfig())
		require.NoError(t, err)
	})
}

// testPolicy is a mock policy for testing
type testPolicy struct {
	checkDeadFunc func(context.Context, HealthCheckFunc) error
}

func (p *testPolicy) DiscoveryUpdate(added, removed, unchanged []*Connection) error {
	return nil
}

func (p *testPolicy) CheckDead(ctx context.Context, healthCheck HealthCheckFunc) error {
	if p.checkDeadFunc != nil {
		return p.checkDeadFunc(ctx, healthCheck)
	}
	return nil
}

func (p *testPolicy) IsEnabled() bool {
	return true
}

//nolint:nilnil // Test stub - Eval is never called in tests, only CheckDead/DiscoveryUpdate are tested
func (p *testPolicy) Eval(ctx context.Context, req *http.Request) (ConnectionPool, error) {
	return nil, nil
}

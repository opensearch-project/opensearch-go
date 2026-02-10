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
	"time"

	"github.com/stretchr/testify/require"
)

func TestNullPolicy(t *testing.T) {
	t.Run("NewNullPolicy creates policy", func(t *testing.T) {
		policy := NewNullPolicy()
		require.NotNil(t, policy)
		require.IsType(t, &NullPolicy{}, policy)
	})

	t.Run("IsEnabled always returns true", func(t *testing.T) {
		policy := NewNullPolicy()
		require.True(t, policy.IsEnabled())
	})

	t.Run("Eval always returns nil, nil", func(t *testing.T) {
		policy := NewNullPolicy()
		ctx := context.Background()
		req, _ := http.NewRequest("GET", "/", nil)

		pool, err := policy.Eval(ctx, req)
		require.Nil(t, pool)
		require.Nil(t, err)
	})

	t.Run("DiscoveryUpdate is no-op with nil inputs", func(t *testing.T) {
		policy := NewNullPolicy()
		err := policy.DiscoveryUpdate(nil, nil, nil)
		require.NoError(t, err)
	})

	t.Run("DiscoveryUpdate is no-op with connections", func(t *testing.T) {
		policy := NewNullPolicy()
		conn := createTestConnection("http://localhost:9200")
		err := policy.DiscoveryUpdate([]*Connection{conn}, nil, nil)
		require.NoError(t, err)
	})

	t.Run("CheckDead is no-op", func(t *testing.T) {
		policy := NewNullPolicy()
		ctx := context.Background()
		healthCheck := func(ctx context.Context, u *url.URL) (*http.Response, error) { return nil, nil }
		err := policy.CheckDead(ctx, healthCheck)
		require.NoError(t, err)
	})

	t.Run("configurePolicySettings is no-op", func(t *testing.T) {
		policy := NewNullPolicy().(*NullPolicy)
		err := policy.configurePolicySettings(createTestConfig())
		require.NoError(t, err)
	})

	t.Run("configurePolicySettings with various configs", func(t *testing.T) {
		policy := NewNullPolicy().(*NullPolicy)

		configs := []policyConfig{
			createTestConfig(),
			{resurrectTimeoutInitial: 60 * time.Second, resurrectTimeoutFactorCutoff: 5},
			{resurrectTimeoutInitial: 0, resurrectTimeoutFactorCutoff: 0},
		}

		for _, config := range configs {
			err := policy.configurePolicySettings(config)
			require.NoError(t, err)
		}
	})
}

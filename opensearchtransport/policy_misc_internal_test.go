// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPolicyTypeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		policy   policyTyped
		expected string
	}{
		{name: "NullPolicy", policy: &NullPolicy{}, expected: "null"},
		{name: "RoundRobinPolicy", policy: &RoundRobinPolicy{}, expected: "roundrobin"},
		{name: "RolePolicy", policy: &RolePolicy{}, expected: "role"},
		{name: "CoordinatorPolicy", policy: &CoordinatorPolicy{}, expected: "coordinator"},
		{name: "IfEnabledPolicy", policy: &IfEnabledPolicy{}, expected: "ifenabled"},
		{name: "MuxPolicy", policy: &MuxPolicy{}, expected: "mux"},
		{name: "PolicyChain", policy: &PolicyChain{}, expected: "chain"},
		{name: "poolRouter", policy: &poolRouter{}, expected: "router"},
		{name: "IndexRouter", policy: &IndexRouter{}, expected: "index_router"},
		{name: "DocRouter", policy: &DocRouter{}, expected: "document_router"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.expected, tc.policy.policyTypeName())
		})
	}
}

func TestRoundRobinPolicy_RotateStandby_NilPool(t *testing.T) {
	t.Parallel()

	p := &RoundRobinPolicy{}
	n, err := p.RotateStandby(context.Background(), 5)
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

func TestCoordinatorPolicy_PoolSnapshot_NilPool(t *testing.T) {
	t.Parallel()

	p := &CoordinatorPolicy{}
	snap := p.PoolSnapshot()
	require.Equal(t, "coordinator", snap.Name)
}

func TestWithShardExactRouting(t *testing.T) {
	t.Parallel()

	t.Run("true clears skip bit", func(t *testing.T) {
		t.Parallel()
		cfg := routerConfig{routingFeatures: routingSkipShardExact}
		opt := WithShardExactRouting(true)
		opt(&cfg)
		require.True(t, cfg.routingFeatures.shardExactEnabled())
	})

	t.Run("false sets skip bit", func(t *testing.T) {
		t.Parallel()
		cfg := routerConfig{}
		opt := WithShardExactRouting(false)
		opt(&cfg)
		require.False(t, cfg.routingFeatures.shardExactEnabled())
	})
}

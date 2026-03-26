// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Policy stub methods: CheckDead, RotateStandby, policySnapshots, etc.
// ---------------------------------------------------------------------------

func TestDocRouterStubs(t *testing.T) {
	t.Parallel()
	cache := newIndexSlotCache(indexSlotCacheConfig{
		minFanOut:   1,
		maxFanOut:   4,
		decayFactor: defaultDecayFactor,
	})
	p := NewDocRouter(cache, defaultDecayFactor)

	t.Run("CheckDead", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, p.CheckDead(context.Background(), nil))
	})

	t.Run("RotateStandby", func(t *testing.T) {
		t.Parallel()
		n, err := p.RotateStandby(context.Background(), 1)
		require.NoError(t, err)
		require.Zero(t, n)
	})

	t.Run("routerSnapshot", func(t *testing.T) {
		t.Parallel()
		snap := p.routerSnapshot()
		require.NotZero(t, snap.Config)
	})

	t.Run("routerCache", func(t *testing.T) {
		t.Parallel()
		require.Same(t, cache, p.routerCache())
	})
}

func TestIndexRouterStubs(t *testing.T) {
	t.Parallel()
	p := NewIndexRouter(indexSlotCacheConfig{
		minFanOut:   1,
		maxFanOut:   4,
		decayFactor: defaultDecayFactor,
	})

	t.Run("CheckDead", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, p.CheckDead(context.Background(), nil))
	})

	t.Run("RotateStandby", func(t *testing.T) {
		t.Parallel()
		n, err := p.RotateStandby(context.Background(), 1)
		require.NoError(t, err)
		require.Zero(t, n)
	})

	t.Run("IsEnabled", func(t *testing.T) {
		t.Parallel()
		// Default state: not enabled (no connections discovered yet)
		require.False(t, p.IsEnabled())
	})
}

func TestPoolRouterStubs(t *testing.T) {
	t.Parallel()
	inner := NewRoundRobinPolicy()
	cache := newIndexSlotCache(indexSlotCacheConfig{
		minFanOut:   1,
		maxFanOut:   4,
		decayFactor: defaultDecayFactor,
	})
	p := &poolRouter{
		inner: inner,
		cache: cache,
		decay: defaultDecayFactor,
	}

	t.Run("RotateStandby", func(t *testing.T) {
		t.Parallel()
		n, err := p.RotateStandby(context.Background(), 1)
		require.NoError(t, err)
		require.Zero(t, n)
	})

	t.Run("policySnapshots", func(t *testing.T) {
		t.Parallel()
		snaps := p.policySnapshots()
		// Inner RoundRobinPolicy returns a named snapshot even with nil pool
		require.Len(t, snaps, 1)
		require.Equal(t, "roundrobin", snaps[0].Name)
	})

	t.Run("routerSnapshot", func(t *testing.T) {
		t.Parallel()
		snap := p.routerSnapshot()
		require.NotZero(t, snap.Config)
	})
}

func TestMuxPolicyStubs(t *testing.T) {
	t.Parallel()
	// Build a minimal MuxPolicy with one route
	rr := NewRoundRobinPolicy()
	routes := []Route{
		NewRoute("GET /_search", rr).MustBuild(),
	}
	p := NewMuxPolicy(routes).(*MuxPolicy)

	t.Run("RotateStandby", func(t *testing.T) {
		t.Parallel()
		n, err := p.RotateStandby(context.Background(), 1)
		require.NoError(t, err)
		require.Zero(t, n)
	})

	t.Run("policySnapshots", func(t *testing.T) {
		t.Parallel()
		snaps := p.policySnapshots()
		// Route has a RoundRobinPolicy which returns its snapshot
		require.NotNil(t, snaps)
	})
}

func TestIfEnabledPolicyStubs(t *testing.T) {
	t.Parallel()
	trueP := NewRoundRobinPolicy()
	falseP := NewNullPolicy()
	p := NewIfEnabledPolicy(func(_ context.Context, _ *http.Request) bool { return true }, trueP, falseP).(*IfEnabledPolicy)

	t.Run("RotateStandby", func(t *testing.T) {
		t.Parallel()
		n, err := p.RotateStandby(context.Background(), 1)
		require.NoError(t, err)
		require.Zero(t, n)
	})

	t.Run("policySnapshots", func(t *testing.T) {
		t.Parallel()
		snaps := p.policySnapshots()
		// RoundRobinPolicy truePolicy has its own pool
		require.NotNil(t, snaps)
	})
}

func TestCoordinatorPolicyStubs(t *testing.T) {
	t.Parallel()
	p := NewCoordinatorPolicy().(*CoordinatorPolicy)

	t.Run("RotateStandby nil pool", func(t *testing.T) {
		t.Parallel()
		n, err := p.RotateStandby(context.Background(), 1)
		require.NoError(t, err)
		require.Zero(t, n)
	})
}

func TestRolePolicyStubs(t *testing.T) {
	t.Parallel()
	policy, err := NewRolePolicy(RoleData)
	require.NoError(t, err)
	p := policy.(*RolePolicy)

	t.Run("PolicySnapshot nil pool", func(t *testing.T) {
		t.Parallel()
		snap := p.PolicySnapshot()
		require.Equal(t, "role:data", snap.Name)
		require.Zero(t, snap.ActiveCount)
	})
}

// ---------------------------------------------------------------------------
// setEnvOverride on all policy types
// ---------------------------------------------------------------------------

func TestSetEnvOverride_AllPolicies(t *testing.T) {
	t.Parallel()

	cache := newIndexSlotCache(indexSlotCacheConfig{
		minFanOut:   1,
		maxFanOut:   4,
		decayFactor: defaultDecayFactor,
	})

	type envOverrideable interface {
		setEnvOverride(bool)
	}

	policies := map[string]envOverrideable{
		"DocRouter": NewDocRouter(cache, defaultDecayFactor),
		"IfEnabledPolicy": NewIfEnabledPolicy(
			func(_ context.Context, _ *http.Request) bool { return true },
			NewNullPolicy(), nil,
		).(*IfEnabledPolicy),
		"IndexRouter": NewIndexRouter(indexSlotCacheConfig{minFanOut: 1, maxFanOut: 4, decayFactor: defaultDecayFactor}),
		"MuxPolicy":   NewMuxPolicy(nil).(*MuxPolicy),
		"poolRouter":  &poolRouter{inner: NewNullPolicy(), cache: cache, decay: defaultDecayFactor},
	}

	for name, policy := range policies {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Disable then verify
			policy.setEnvOverride(false)
			switch p := policy.(type) {
			case *DocRouter:
				require.False(t, psIsEnabled(p.policyState.Load()))
			case *IfEnabledPolicy:
				require.False(t, psIsEnabled(p.policyState.Load()))
			case *IndexRouter:
				require.False(t, psIsEnabled(p.policyState.Load()))
			case *MuxPolicy:
				require.False(t, psIsEnabled(p.policyState.Load()))
			case *poolRouter:
				require.False(t, psIsEnabled(p.policyState.Load()))
			}

			// Enable then verify
			policy.setEnvOverride(true)
			switch p := policy.(type) {
			case *DocRouter:
				require.NotZero(t, p.policyState.Load()&psEnvEnabled)
				require.Zero(t, p.policyState.Load()&psEnvDisabled)
			case *IfEnabledPolicy:
				require.NotZero(t, p.policyState.Load()&psEnvEnabled)
				require.Zero(t, p.policyState.Load()&psEnvDisabled)
			case *IndexRouter:
				require.NotZero(t, p.policyState.Load()&psEnvEnabled)
				require.Zero(t, p.policyState.Load()&psEnvDisabled)
			case *MuxPolicy:
				require.NotZero(t, p.policyState.Load()&psEnvEnabled)
				require.Zero(t, p.policyState.Load()&psEnvDisabled)
			case *poolRouter:
				require.NotZero(t, p.policyState.Load()&psEnvEnabled)
				require.Zero(t, p.policyState.Load()&psEnvDisabled)
			}
		})
	}
}

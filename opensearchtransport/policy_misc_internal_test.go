// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"testing"
	"time"

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

func TestCoordinatorPolicy_PolicySnapshot_NilPool(t *testing.T) {
	t.Parallel()

	p := &CoordinatorPolicy{}
	snap := p.PolicySnapshot()
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

func TestWithShardCosts(t *testing.T) {
	t.Parallel()

	type kv struct {
		idx   shardCostIndex
		value float64
	}

	// Shared connection for scoreFunc behavioral checks. With load=0 and no
	// in-flight requests, utilization = (0+1)/1 = 1.0, so the score reduces
	// to rtt * effectiveCost; rtt is the RTT-bucketed median of a known
	// fixed input, which is stable across calls.
	conn := scoreTestConn(t, "node-shard-cost", 200*time.Microsecond, 0)
	rtt := float64(conn.rttRing.medianBucket())

	// At primaryPct=1.0 on an idle connection (writeUtil=0), the dynamic
	// formula reduces to: effectiveCost = base + amplify*(0^exponent) = base.
	// This isolates curveBase as the only observable curve param.
	const epsilon = 1e-9

	tests := []struct {
		name    string
		initial string // pre-existing shardCostSpec value
		spec    string // argument to WithShardCosts
		want    string // expected shardCostSpec after applying
		// wantReads/wantWrites are slot-specific overrides expected in the
		// parsed shardCostConfig. Slots not listed must still equal the
		// compile-time defaults from shardCostForReads/shardCostForWrites.
		wantReads  []kv
		wantWrites []kv
		// wantBase is the expected curveBase parameter, observed via
		// scoreFunc(conn, _, primaryPct=1.0, _, _) at idle. Defaults to
		// defaultReadCurveBase when zero (sentinel: never specify 0
		// directly, since r:base=0 is rejected by the parser).
		wantBase float64
	}{
		{
			name:       "stores spec in config",
			spec:       "replica=1.0,write_primary=0.5",
			want:       "replica=1.0,write_primary=0.5",
			wantReads:  []kv{{shardCostReplica, 1.0}},
			wantWrites: []kv{{shardCostPrimary, 0.5}},
			wantBase:   defaultReadCurveBase, // no curve key set
		},
		{
			name:     "bare numeric sets r:base",
			spec:     "1.5",
			want:     "1.5",
			wantBase: 1.5,
		},
		{
			name:     "prefixed keys set curve params",
			spec:     "r:base=0.9,r:amplify=2.5",
			want:     "r:base=0.9,r:amplify=2.5",
			wantBase: 0.9,
		},
		{
			name:     "empty spec clears config",
			initial:  "replica=2.0",
			spec:     "",
			want:     "",
			wantBase: defaultReadCurveBase,
		},
		{
			name:       "overwrites previous value",
			initial:    "replica=2.0",
			spec:       "write_replica=3.0",
			want:       "write_replica=3.0",
			wantWrites: []kv{{shardCostReplica, 3.0}},
			wantBase:   defaultReadCurveBase,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := routerConfig{shardCostSpec: tt.initial}
			WithShardCosts(tt.spec)(&cfg)
			require.Equal(t, tt.want, cfg.shardCostSpec)

			// Round-trip the stored spec through the parser. WithShardCosts
			// stores the spec verbatim, so without exercising the parser a
			// stale or typo'd spec would only surface at router construction
			// time. The assertions below verify that each requested override
			// lands in the expected static table slot, and that scoreFunc
			// reflects the curve params from the spec.
			parsed, err := parseShardCostConfig(cfg.shardCostSpec)
			require.NoError(t, err)

			// Build expected static tables: start from compile-time defaults
			// and overlay the per-case overrides. Slots not listed must
			// retain their default value.
			wantReads := shardCostForReads
			for _, exp := range tt.wantReads {
				wantReads[exp.idx] = exp.value
			}
			wantWrites := shardCostForWrites
			for _, exp := range tt.wantWrites {
				wantWrites[exp.idx] = exp.value
			}
			for i := range wantReads {
				require.InDelta(t, wantReads[i], parsed.reads[i], epsilon, "reads[%d]", i)
			}
			for i := range wantWrites {
				require.InDelta(t, wantWrites[i], parsed.writes[i], epsilon, "writes[%d]", i)
			}

			// Exercise scoreFunc to confirm curveBase was threaded into the
			// closure. At primaryPct=1.0 on an idle conn the effective cost
			// is exactly curveBase, so score = rtt * 1.0 * base.
			require.NotNil(t, parsed.scoreFunc)
			score := parsed.scoreFunc(conn, 1.0, 1.0, "", true)
			require.InDelta(t, rtt*tt.wantBase, score, 0.001)
		})
	}
}

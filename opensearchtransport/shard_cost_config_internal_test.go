// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseShardCostConfig(t *testing.T) {
	t.Parallel()

	t.Run("empty string returns defaults", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseShardCostConfig("")
		require.NoError(t, err)
		require.Equal(t, shardCostForReads, cfg.reads)
		require.Equal(t, shardCostForWrites, cfg.writes)
		require.NotNil(t, cfg.scoreFunc)
	})

	t.Run("whitespace-only returns defaults", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseShardCostConfig("   ")
		require.NoError(t, err)
		require.Equal(t, shardCostForReads, cfg.reads)
		require.Equal(t, shardCostForWrites, cfg.writes)
		require.NotNil(t, cfg.scoreFunc)
	})

	t.Run("bare numeric sets r:base", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseShardCostConfig("3.0")
		require.NoError(t, err)
		// Static tables unchanged.
		require.Equal(t, shardCostForReads, cfg.reads)
		require.Equal(t, shardCostForWrites, cfg.writes)
		// scoreFunc is non-nil (base=3.0).
		require.NotNil(t, cfg.scoreFunc)
	})

	t.Run("bare zero uses defaults (no change)", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseShardCostConfig("0")
		require.NoError(t, err)
		require.Equal(t, shardCostForReads, cfg.reads)
		require.Equal(t, shardCostForWrites, cfg.writes)
	})

	t.Run("bare negative uses defaults (no change)", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseShardCostConfig("-1")
		require.NoError(t, err)
		require.Equal(t, shardCostForReads, cfg.reads)
		require.Equal(t, shardCostForWrites, cfg.writes)
	})

	t.Run("r: curve keys", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseShardCostConfig("r:base=0.8,r:amplify=3.0,r:exponent=1.5")
		require.NoError(t, err)
		// Static tables unchanged.
		require.Equal(t, shardCostForReads, cfg.reads)
		require.Equal(t, shardCostForWrites, cfg.writes)
		// scoreFunc is non-nil with custom curve.
		require.NotNil(t, cfg.scoreFunc)
	})

	t.Run("static key: unknown sets both tables", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseShardCostConfig("unknown=64.0")
		require.NoError(t, err)
		require.InDelta(t, 64.0, cfg.reads[shardCostUnknown], 0)
		require.InDelta(t, 64.0, cfg.writes[shardCostUnknown], 0)
	})

	t.Run("static key: relocating sets both tables", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseShardCostConfig("relocating=12.0")
		require.NoError(t, err)
		require.InDelta(t, 12.0, cfg.reads[shardCostRelocating], 0)
		require.InDelta(t, 12.0, cfg.writes[shardCostRelocating], 0)
	})

	t.Run("static key: initializing sets both tables", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseShardCostConfig("initializing=24.0")
		require.NoError(t, err)
		require.InDelta(t, 24.0, cfg.reads[shardCostInitializing], 0)
		require.InDelta(t, 24.0, cfg.writes[shardCostInitializing], 0)
	})

	t.Run("static key: replica sets reads only", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseShardCostConfig("replica=5.0")
		require.NoError(t, err)
		require.InDelta(t, 5.0, cfg.reads[shardCostReplica], 0)
		// Writes replica unchanged.
		require.InDelta(t, costWriteReplica, cfg.writes[shardCostReplica], 0)
	})

	t.Run("static key: write_primary sets writes only", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseShardCostConfig("write_primary=0.5")
		require.NoError(t, err)
		require.InDelta(t, 0.5, cfg.writes[shardCostPrimary], 0)
		// Reads primary unchanged.
		require.InDelta(t, costReadPrimary, cfg.reads[shardCostPrimary], 0)
	})

	t.Run("static key: write_replica sets writes only", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseShardCostConfig("write_replica=3.0")
		require.NoError(t, err)
		require.InDelta(t, 3.0, cfg.writes[shardCostReplica], 0)
		// Reads replica unchanged.
		require.InDelta(t, costReadReplica, cfg.reads[shardCostReplica], 0)
	})

	t.Run("mixed static and curve keys", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseShardCostConfig("r:base=0.9,unknown=50.0,write_primary=0.8")
		require.NoError(t, err)
		require.InDelta(t, 50.0, cfg.reads[shardCostUnknown], 0)
		require.InDelta(t, 50.0, cfg.writes[shardCostUnknown], 0)
		require.InDelta(t, 0.8, cfg.writes[shardCostPrimary], 0)
		require.NotNil(t, cfg.scoreFunc)
	})

	t.Run("zero value in key=value falls back to default", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseShardCostConfig("unknown=0,relocating=5.0")
		require.NoError(t, err)
		// unknown=0 → clamped to default.
		require.InDelta(t, costUnknown, cfg.reads[shardCostUnknown], 0)
		require.InDelta(t, costUnknown, cfg.writes[shardCostUnknown], 0)
		// relocating=5.0 → set.
		require.InDelta(t, 5.0, cfg.reads[shardCostRelocating], 0)
		require.InDelta(t, 5.0, cfg.writes[shardCostRelocating], 0)
	})

	t.Run("negative value in key=value falls back to default", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseShardCostConfig("unknown=-1.0")
		require.NoError(t, err)
		require.InDelta(t, costUnknown, cfg.reads[shardCostUnknown], 0)
		require.InDelta(t, costUnknown, cfg.writes[shardCostUnknown], 0)
	})

	t.Run("trailing comma is ignored", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseShardCostConfig("replica=3.0,")
		require.NoError(t, err)
		require.InDelta(t, 3.0, cfg.reads[shardCostReplica], 0)
	})

	t.Run("invalid specs", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			spec   string
			key    string
			reason string
			hasErr bool // true if Err (wrapped error) should be non-nil
		}{
			{
				name:   "unknown key",
				spec:   "bogus=1.0",
				key:    "bogus",
				reason: "unknown key",
			},
			{
				name:   "old abstract key rejected",
				spec:   "preferred=1.0",
				key:    "preferred",
				reason: "unknown key",
			},
			{
				name:   "old w: prefix rejected",
				spec:   "w:primary=1.0",
				key:    "w:primary",
				reason: "unknown key",
			},
			{
				name:   "unparseable float",
				spec:   "r:base=abc",
				key:    "r:base",
				reason: "invalid value",
				hasErr: true,
			},
			{
				name:   "missing value (no equals sign)",
				spec:   "r:base",
				key:    "r:base",
				reason: "missing value",
			},
			{
				name:   "empty value after equals",
				spec:   "r:amplify=",
				key:    "r:amplify",
				reason: "invalid value",
				hasErr: true,
			},
			{
				name:   "valid key followed by invalid key",
				spec:   "r:base=0.9,bogus=2.0",
				key:    "bogus",
				reason: "unknown key",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, err := parseShardCostConfig(tt.spec)
				var scErr *ShardCostConfigError
				require.ErrorAs(t, err, &scErr)
				require.Equal(t, tt.key, scErr.Key)
				require.Equal(t, tt.reason, scErr.Reason)
				if tt.hasErr {
					require.Error(t, scErr.Err)
				} else {
					require.NoError(t, scErr.Err)
				}
			})
		}
	})
}

func TestShardCostConfigError(t *testing.T) {
	t.Parallel()

	t.Run("Error", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			err  ShardCostConfigError
			want string
		}{
			{
				name: "all fields populated",
				err: ShardCostConfigError{
					Key: "bogus", Reason: "invalid value",
					Detail: "expected float", Err: strconv.ErrSyntax,
				},
				want: `shard cost config: invalid value for key "bogus": expected float: invalid syntax`,
			},
			{
				name: "reason only",
				err:  ShardCostConfigError{Reason: "missing value"},
				want: "shard cost config: missing value",
			},
			{
				name: "reason and key",
				err:  ShardCostConfigError{Key: "r:base", Reason: "unknown key"},
				want: `shard cost config: unknown key for key "r:base"`,
			},
			{
				name: "reason and detail",
				err:  ShardCostConfigError{Reason: "unknown key", Detail: "valid keys: a, b"},
				want: "shard cost config: unknown key: valid keys: a, b",
			},
			{
				name: "reason and wrapped error",
				err:  ShardCostConfigError{Reason: "invalid value", Err: strconv.ErrSyntax},
				want: "shard cost config: invalid value: invalid syntax",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				require.Equal(t, tt.want, tt.err.Error())
			})
		}
	})

	t.Run("Unwrap", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			err     ShardCostConfigError
			wantNil bool
			target  error // expected wrapped error for ErrorIs check
		}{
			{
				name:   "returns wrapped error",
				err:    ShardCostConfigError{Reason: "invalid value", Err: strconv.ErrSyntax},
				target: strconv.ErrSyntax,
			},
			{
				name:    "returns nil when no wrapped error",
				err:     ShardCostConfigError{Reason: "unknown key"},
				wantNil: true,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				if tt.wantNil {
					require.NoError(t, errors.Unwrap(&tt.err))
				} else {
					require.ErrorIs(t, &tt.err, tt.target)
				}
			})
		}
	})
}

func TestNewReadScoreFunc(t *testing.T) {
	t.Parallel()

	// Create a minimal connection for testing. With 0 load and no
	// in-flight requests, utilization = (0+1)/1 = 1.0, so
	// score = rtt * 1.0 * effectiveCost.
	conn := scoreTestConn(t, "node-1", 200*time.Microsecond, 0)
	rtt := float64(conn.rttRing.medianBucket())

	tests := []struct {
		name       string
		base       float64
		amplify    float64
		exponent   float64
		shardCost  float64
		primaryPct float64
		wantCost   float64 // effective cost multiplier (score = rtt * util * wantCost)
	}{
		{
			name:       "pure replica uses static cost",
			base:       0.95,
			amplify:    2.0,
			exponent:   2.0,
			shardCost:  1.0,
			primaryPct: 0.0,
			wantCost:   1.0, // no primary blend, straight shardCost
		},
		{
			name:       "pure primary at idle uses base cost",
			base:       0.95,
			amplify:    2.0,
			exponent:   2.0,
			shardCost:  1.0,
			primaryPct: 1.0,
			wantCost:   0.95, // 100% primary, idle write pool -> base
		},
		{
			name:       "mixed 30% primary blends proportionally",
			base:       0.95,
			amplify:    2.0,
			exponent:   2.0,
			shardCost:  1.0,
			primaryPct: 0.3,
			wantCost:   0.7*1.0 + 0.3*0.95, // 0.985
		},
		{
			name:       "mixed 50% primary blends to midpoint",
			base:       0.95,
			amplify:    2.0,
			exponent:   2.0,
			shardCost:  1.0,
			primaryPct: 0.5,
			wantCost:   0.5*1.0 + 0.5*0.95, // 0.975
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fn := newReadScoreFunc(tt.base, tt.amplify, tt.exponent)
			score := fn(conn, tt.shardCost, tt.primaryPct, "", true)
			require.InDelta(t, rtt*1.0*tt.wantCost, score, 0.001)
		})
	}

	// Verify non-primary matches the static scoring path exactly.
	t.Run("non-primary matches calcConnDefaultScore", func(t *testing.T) {
		t.Parallel()
		fn := newReadScoreFunc(0.95, 2.0, 2.0)
		staticScore := calcConnDefaultScore(conn, 1.0, "", true)
		dynScore := fn(conn, 1.0, 0.0, "", true)
		require.InDelta(t, staticScore, dynScore, 0.001)
	})
}

func TestCalcNodePrimaryPct(t *testing.T) {
	t.Parallel()

	t.Run("nil returns 0", func(t *testing.T) {
		t.Parallel()
		require.InDelta(t, 0.0, calcNodePrimaryPct(nil), 0)
	})

	t.Run("zero shards returns 0", func(t *testing.T) {
		t.Parallel()
		require.InDelta(t, 0.0, calcNodePrimaryPct(&shardNodeInfo{}), 0)
	})

	t.Run("pure replica returns 0", func(t *testing.T) {
		t.Parallel()
		require.InDelta(t, 0.0, calcNodePrimaryPct(&shardNodeInfo{Replicas: 5}), 0)
	})

	t.Run("pure primary returns 1", func(t *testing.T) {
		t.Parallel()
		require.InDelta(t, 1.0, calcNodePrimaryPct(&shardNodeInfo{Primaries: 3}), 0)
	})

	t.Run("mixed 3P/7R returns 0.3", func(t *testing.T) {
		t.Parallel()
		require.InDelta(t, 0.3, calcNodePrimaryPct(&shardNodeInfo{Primaries: 3, Replicas: 7}), 0.001)
	})

	t.Run("mixed 1P/1R returns 0.5", func(t *testing.T) {
		t.Parallel()
		require.InDelta(t, 0.5, calcNodePrimaryPct(&shardNodeInfo{Primaries: 1, Replicas: 1}), 0)
	})
}

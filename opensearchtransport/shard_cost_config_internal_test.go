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

	type kv struct {
		idx  shardCostIndex
		want float64
	}

	tests := []struct {
		name string
		spec string

		// Error expectations.
		wantErr    bool
		errKey     string
		errReason  string
		errHasWrap bool // true if ShardCostConfigError.Err should be non-nil

		// Success expectations.
		wantDefaults bool // cfg.reads/writes equal the compile-time default tables
		wantReads    []kv // assert these reads slots
		wantWrites   []kv // assert these writes slots
	}{
		{
			name:         "empty string returns defaults",
			spec:         "",
			wantDefaults: true,
		},
		{
			name:         "whitespace-only returns defaults",
			spec:         "   ",
			wantDefaults: true,
		},
		{
			name:         "bare numeric sets r:base",
			spec:         "3.0",
			wantDefaults: true, // static tables unchanged
		},
		{
			name:      "bare zero rejected",
			spec:      "0",
			wantErr:   true,
			errReason: shardCostMessageNotFinite,
		},
		{
			name:      "bare negative rejected",
			spec:      "-1",
			wantErr:   true,
			errReason: shardCostMessageNotFinite,
		},
		{
			name:      "bare +Inf rejected",
			spec:      "+Inf",
			wantErr:   true,
			errReason: shardCostMessageNotFinite,
		},
		{
			name:      "bare NaN rejected",
			spec:      "NaN",
			wantErr:   true,
			errReason: shardCostMessageNotFinite,
		},
		{
			name:         "r: curve keys",
			spec:         "r:base=0.8,r:amplify=3.0,r:exponent=1.5",
			wantDefaults: true, // static tables unchanged
		},
		{
			name:       "static key: unknown sets both tables",
			spec:       "unknown=64.0",
			wantReads:  []kv{{shardCostUnknown, 64.0}},
			wantWrites: []kv{{shardCostUnknown, 64.0}},
		},
		{
			name:       "static key: relocating sets both tables",
			spec:       "relocating=12.0",
			wantReads:  []kv{{shardCostRelocating, 12.0}},
			wantWrites: []kv{{shardCostRelocating, 12.0}},
		},
		{
			name:       "static key: initializing sets both tables",
			spec:       "initializing=24.0",
			wantReads:  []kv{{shardCostInitializing, 24.0}},
			wantWrites: []kv{{shardCostInitializing, 24.0}},
		},
		{
			name:       "static key: replica sets reads only",
			spec:       "replica=5.0",
			wantReads:  []kv{{shardCostReplica, 5.0}},
			wantWrites: []kv{{shardCostReplica, costWriteReplica}},
		},
		{
			name:       "static key: write_primary sets writes only",
			spec:       "write_primary=0.5",
			wantReads:  []kv{{shardCostPrimary, costReadPrimary}},
			wantWrites: []kv{{shardCostPrimary, 0.5}},
		},
		{
			name:       "static key: write_replica sets writes only",
			spec:       "write_replica=3.0",
			wantReads:  []kv{{shardCostReplica, costReadReplica}},
			wantWrites: []kv{{shardCostReplica, 3.0}},
		},
		{
			name:       "mixed static and curve keys",
			spec:       "r:base=0.9,unknown=50.0,write_primary=0.8",
			wantReads:  []kv{{shardCostUnknown, 50.0}},
			wantWrites: []kv{{shardCostUnknown, 50.0}, {shardCostPrimary, 0.8}},
		},
		{
			name:       "zero value in key=value falls back to default",
			spec:       "unknown=0,relocating=5.0",
			wantReads:  []kv{{shardCostUnknown, costUnknown}, {shardCostRelocating, 5.0}},
			wantWrites: []kv{{shardCostUnknown, costUnknown}, {shardCostRelocating, 5.0}},
		},
		{
			name:         "negative value in key=value falls back to default",
			spec:         "unknown=-1.0",
			wantDefaults: true,
		},
		{
			name:      "trailing comma is ignored",
			spec:      "replica=3.0,",
			wantReads: []kv{{shardCostReplica, 3.0}},
		},
		{
			name:      "unknown key",
			spec:      "bogus=1.0",
			wantErr:   true,
			errKey:    "bogus",
			errReason: shardCostMessageUnknownKey,
		},
		{
			name:      "old abstract key rejected",
			spec:      "preferred=1.0",
			wantErr:   true,
			errKey:    "preferred",
			errReason: shardCostMessageUnknownKey,
		},
		{
			name:      "old w: prefix rejected",
			spec:      "w:primary=1.0",
			wantErr:   true,
			errKey:    "w:primary",
			errReason: shardCostMessageUnknownKey,
		},
		{
			name:       "unparseable float",
			spec:       "r:base=abc",
			wantErr:    true,
			errKey:     shardCostKeyBase,
			errReason:  shardCostMessageInvalid,
			errHasWrap: true,
		},
		{
			name:      "missing value (no equals sign)",
			spec:      "r:base",
			wantErr:   true,
			errKey:    shardCostKeyBase,
			errReason: shardCostMessageMissing,
		},
		{
			name:       "empty value after equals",
			spec:       "r:amplify=",
			wantErr:    true,
			errKey:     shardCostKeyAmplify,
			errReason:  shardCostMessageInvalid,
			errHasWrap: true,
		},
		{
			name:      "valid key followed by invalid key",
			spec:      "r:base=0.9,bogus=2.0",
			wantErr:   true,
			errKey:    "bogus",
			errReason: shardCostMessageUnknownKey,
		},
		{
			name:      "r:base NaN rejected",
			spec:      "r:base=NaN",
			wantErr:   true,
			errKey:    shardCostKeyBase,
			errReason: shardCostMessageNotFinite,
		},
		{
			name:      "r:amplify +Inf rejected",
			spec:      "r:amplify=+Inf",
			wantErr:   true,
			errKey:    shardCostKeyAmplify,
			errReason: shardCostMessageNotFinite,
		},
		{
			name:      "r:exponent negative rejected",
			spec:      "r:exponent=-2.0",
			wantErr:   true,
			errKey:    shardCostKeyExponent,
			errReason: shardCostMessageNotFinite,
		},
		{
			name:      "r:base zero rejected",
			spec:      "r:base=0",
			wantErr:   true,
			errKey:    shardCostKeyBase,
			errReason: shardCostMessageNotFinite,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := parseShardCostConfig(tt.spec)
			if tt.wantErr {
				var scErr *ShardCostConfigError
				require.ErrorAs(t, err, &scErr)
				require.Equal(t, tt.errKey, scErr.Key)
				require.ErrorContains(t, err, tt.errReason)
				if tt.errHasWrap {
					require.Error(t, scErr.Err)
				} else {
					require.NoError(t, scErr.Err)
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, cfg.scoreFunc)
			if tt.wantDefaults {
				require.Equal(t, shardCostForReads, cfg.reads)
				require.Equal(t, shardCostForWrites, cfg.writes)
			}
			for _, f := range tt.wantReads {
				require.InDelta(t, f.want, cfg.reads[f.idx], 0)
			}
			for _, f := range tt.wantWrites {
				require.InDelta(t, f.want, cfg.writes[f.idx], 0)
			}
		})
	}
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
					Key: "bogus", Message: shardCostMessageInvalid + ": expected float", Err: strconv.ErrSyntax,
				},
				want: `shard cost config: invalid value: expected float for key "bogus": invalid syntax`,
			},
			{
				name: "message only",
				err:  ShardCostConfigError{Message: shardCostMessageMissing},
				want: "shard cost config: missing value",
			},
			{
				name: "message and key",
				err:  ShardCostConfigError{Key: shardCostKeyBase, Message: shardCostMessageUnknownKey},
				want: `shard cost config: unknown key for key "r:base"`,
			},
			{
				name: "message embeds detail",
				err:  ShardCostConfigError{Message: shardCostMessageUnknownKey + ": valid keys: a, b"},
				want: "shard cost config: unknown key: valid keys: a, b",
			},
			{
				name: "message and wrapped error",
				err:  ShardCostConfigError{Message: shardCostMessageInvalid, Err: strconv.ErrSyntax},
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
				err:    ShardCostConfigError{Message: shardCostMessageInvalid, Err: strconv.ErrSyntax},
				target: strconv.ErrSyntax,
			},
			{
				name:    "returns nil when no wrapped error",
				err:     ShardCostConfigError{Message: shardCostMessageUnknownKey},
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

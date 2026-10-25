// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
	sched "github.com/opensearch-project/opensearch-go/v5/internal/scheduler"
)

// envLookup answers from env instead of the process environment, so the tests
// that use it can run in parallel.
func envLookup(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := env[name]

		return v, ok
	}
}

func TestBulkSizingEnvShard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		shard     ShardSizing
		env       string
		wantShard ShardSizing
	}{
		{name: "unset stays unset, which is pid", wantShard: ShardUnset},
		{name: "unset keeps pid", shard: ShardPID, wantShard: ShardPID},
		{name: "unset keeps fixed", shard: ShardFixed, wantShard: ShardFixed},
		{name: "fixed overrides pid", shard: ShardPID, env: "fixed", wantShard: ShardFixed},
		{name: "fixed overrides the zero value", env: "fixed", wantShard: ShardFixed},
		{name: "pid overrides fixed", shard: ShardFixed, env: "pid", wantShard: ShardPID},
		{name: "case and space are ignored", env: "  FIXED ", wantShard: ShardFixed},
		{name: "a node setting is unknown here", shard: ShardFixed, env: "aimd", wantShard: ShardFixed},
		{name: "unset is not a value", shard: ShardFixed, env: "unset", wantShard: ShardFixed},
		{name: "an empty value is ignored", shard: ShardFixed, env: "", wantShard: ShardFixed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := BulkSizing{Shard: tt.shard}.resolveWith(testCeiling, envLookup(map[string]string{envvars.BulkShard: tt.env}))
			require.Equal(t, tt.wantShard, r.shard)
			require.Equal(t, tt.wantShard == ShardFixed, r.fixed)
			require.False(t, r.targetFixed, "the shard setting leaves the node window alone")
			if tt.wantShard == ShardFixed {
				require.InDelta(t, testCeiling, r.initial, 0, "a fixed size starts at the ceiling")
				require.InDelta(t, testCeiling/sizingMinDivisor, r.min, 0, "a fixed size keeps the minimum the controller would use")
			}
		})
	}
}

func TestBulkSizingEnvNode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		node     TargetSizing
		env      string
		wantNode TargetSizing
	}{
		{name: "unset stays unset, which is aimd", wantNode: TargetUnset},
		{name: "unset keeps aimd", node: TargetAIMD, wantNode: TargetAIMD},
		{name: "unset keeps fixed", node: TargetFixed, wantNode: TargetFixed},
		{name: "fixed overrides aimd", node: TargetAIMD, env: "fixed", wantNode: TargetFixed},
		{name: "aimd overrides fixed", node: TargetFixed, env: "aimd", wantNode: TargetAIMD},
		{name: "case and space are ignored", env: " Fixed", wantNode: TargetFixed},
		{name: "a shard setting is unknown here", node: TargetFixed, env: "pid", wantNode: TargetFixed},
		{name: "an empty value is ignored", node: TargetFixed, env: "", wantNode: TargetFixed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := BulkSizing{Target: tt.node}.resolveWith(testCeiling, envLookup(map[string]string{envvars.BulkTarget: tt.env}))
			require.Equal(t, tt.wantNode, r.node)
			require.Equal(t, tt.wantNode == TargetFixed, r.targetFixed)
			require.False(t, r.fixed, "the node setting leaves the shard buffers alone")
		})
	}
}

func TestSizingString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setting fmt.Stringer
		want    string
	}{
		{ShardUnset, "unset"},
		{ShardPID, "pid"},
		{ShardFixed, "fixed"},
		{ShardSizing(numShardSizings), "ShardSizing(3)"},
		{TargetUnset, "unset"},
		{TargetAIMD, "aimd"},
		{TargetFixed, "fixed"},
		{TargetSizing(numTargetSizings), "TargetSizing(3)"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%T %d", tt.setting, tt.setting), func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, tt.setting.String())
		})
	}
}

func TestBulkSizingEnvPIDGains(t *testing.T) {
	t.Parallel()

	defaults := [3]float64{sizingDefaultKp, sizingDefaultKi, sizingDefaultKd}

	tests := []struct {
		name   string
		sizing BulkSizing
		env    map[string]string
		want   [3]float64
	}{
		{
			name: "each gain overrides on its own",
			env:  map[string]string{envvars.BulkPIDKi: "0.5"},
			want: [3]float64{sizingDefaultKp, 0.5, sizingDefaultKd},
		},
		{
			name:   "they override programmatic gains",
			sizing: BulkSizing{PID: PIDGains{Kp: 1, Ki: 1, Kd: 1}},
			env:    map[string]string{envvars.BulkPIDKp: "3", envvars.BulkPIDKi: "4", envvars.BulkPIDKd: "5"},
			want:   [3]float64{3, 4, 5},
		},
		{
			name: "zero turns a term off",
			env:  map[string]string{envvars.BulkPIDKi: "0", envvars.BulkPIDKd: "0"},
			want: [3]float64{sizingDefaultKp, 0, 0},
		},
		{name: "text is ignored", env: map[string]string{envvars.BulkPIDKp: "abc"}, want: defaults},
		{name: "a negative gain is ignored", env: map[string]string{envvars.BulkPIDKp: "-1"}, want: defaults},
		{name: "NaN is ignored", env: map[string]string{envvars.BulkPIDKp: "NaN"}, want: defaults},
		{name: "infinity is ignored", env: map[string]string{envvars.BulkPIDKp: "Inf"}, want: defaults},
		{name: "positive infinity is ignored", env: map[string]string{envvars.BulkPIDKp: "+Inf"}, want: defaults},
		{name: "empty is ignored", env: map[string]string{envvars.BulkPIDKp: ""}, want: defaults},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := tt.sizing.resolveWith(testCeiling, envLookup(tt.env))
			want, got := tt.want, [3]float64{r.kp, r.ki, r.kd}
			require.InDeltaSlice(t, want[:], got[:], 1e-9, "kp, ki, kd")
		})
	}
}

func TestBulkIndexerByteWindowStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		sizing BulkSizing
		// nodeBytes is TargetBytes in the config, 0 for unset.
		nodeBytes int
		// wantCeiling is where the window starts and the most it grows to, wantFixed
		// whether it can move.
		wantCeiling float64
		wantFixed   bool
	}{
		{name: "an unset TargetBytes leaves the window as wide as the job", wantCeiling: 64 << 20},
		{name: "TargetBytes caps the window", nodeBytes: 8 << 20, wantCeiling: 8 << 20},
		{name: "TargetBytes is held to the job", nodeBytes: 1 << 40, wantCeiling: 64 << 20},
		{
			name: "a fixed node holds the window where it started", sizing: BulkSizing{Target: TargetFixed},
			nodeBytes: 8 << 20, wantCeiling: 8 << 20, wantFixed: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(2)}, "n0", "n1")
			bi := newBulkClient(t, newFakeServer(cluster), BulkIndexerConfig{
				NumWorkers: 1, FlushBytes: 1 << 20, MaxMemoryBytes: 64 << 20, TargetBytes: tt.nodeBytes, FlushInterval: time.Hour,
				Sizing: tt.sizing,
			}).(*bulkClient)

			require.InDelta(t, tt.wantCeiling, bi.window.Start, 0, "it starts wide open")
			require.InDelta(t, tt.wantCeiling, bi.window.Ceiling, 0)
			require.Equal(t, tt.wantFixed, bi.window.Floor >= bi.window.Ceiling, "a fixed window's floor is its ceiling")
		})
	}
}

// TestBulkSizingControllersAreIndependent: fixing the shard buffers leaves the node
// window answering 429s, and fixing the node window leaves the shard buffers
// adaptive.
func TestBulkSizingControllersAreIndependent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		sizing          BulkSizing
		wantShardFixed  bool
		wantTargetFixed bool
	}{
		{name: "the defaults", sizing: BulkSizing{}},
		{name: "fixed shards, adaptive window", sizing: BulkSizing{Shard: ShardFixed}, wantShardFixed: true},
		{name: "adaptive shards, fixed window", sizing: BulkSizing{Target: TargetFixed}, wantTargetFixed: true},
		{name: "both fixed", sizing: BulkSizing{Shard: ShardFixed, Target: TargetFixed}, wantShardFixed: true, wantTargetFixed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(2)}, "n0")
			bi := newBulkClient(t, newFakeServer(cluster), BulkIndexerConfig{
				NumWorkers: 1, FlushBytes: 1 << 20, MaxMemoryBytes: 64 << 20, FlushInterval: time.Hour, Sizing: tt.sizing,
			}).(*bulkClient)
			t.Cleanup(func() { _ = bi.Close(t.Context()) })

			require.Equal(t, tt.wantShardFixed, bi.sizing.fixed, "shard buffers fixed")
			require.Equal(t, tt.wantTargetFixed, bi.window.Floor >= bi.window.Ceiling, "node window fixed")

			// A 429 shrinks the window unless the window is fixed, whatever the shards do.
			w := sched.NewWindow(bi.window)
			require.Equal(t, !tt.wantTargetFixed, w.Shrink(int64(bi.window.Start), 0))
		})
	}
}

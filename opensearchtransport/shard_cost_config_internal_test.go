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

	"github.com/stretchr/testify/require"
)

func TestParseShardCostConfig(t *testing.T) {
	t.Parallel()

	t.Run("empty string returns defaults", func(t *testing.T) {
		t.Parallel()
		reads, writes, err := parseShardCostConfig("")
		require.NoError(t, err)
		require.Equal(t, shardCostForReads, reads)
		require.Equal(t, shardCostForWrites, writes)
	})

	t.Run("whitespace-only returns defaults", func(t *testing.T) {
		t.Parallel()
		reads, writes, err := parseShardCostConfig("   ")
		require.NoError(t, err)
		require.Equal(t, shardCostForReads, reads)
		require.Equal(t, shardCostForWrites, writes)
	})

	t.Run("bare numeric sets preferred and alternate in both tables", func(t *testing.T) {
		t.Parallel()
		reads, writes, err := parseShardCostConfig("1.5")
		require.NoError(t, err)

		// Reads: preferred = replica, alternate = primary.
		require.InDelta(t, 1.5, reads[shardCostReplica], 0)
		require.InDelta(t, 1.5, reads[shardCostPrimary], 0)
		// Others unchanged.
		require.InDelta(t, costUnknown, reads[shardCostUnknown], 0)
		require.InDelta(t, costRelocating, reads[shardCostRelocating], 0)
		require.InDelta(t, costInitializing, reads[shardCostInitializing], 0)

		// Writes: preferred = primary, alternate = replica.
		require.InDelta(t, 1.5, writes[shardCostPrimary], 0)
		require.InDelta(t, 1.5, writes[shardCostReplica], 0)
		// Others unchanged.
		require.InDelta(t, costUnknown, writes[shardCostUnknown], 0)
		require.InDelta(t, costRelocating, writes[shardCostRelocating], 0)
		require.InDelta(t, costInitializing, writes[shardCostInitializing], 0)
	})

	t.Run("bare zero uses defaults (no change)", func(t *testing.T) {
		t.Parallel()
		reads, writes, err := parseShardCostConfig("0")
		require.NoError(t, err)
		require.Equal(t, shardCostForReads, reads)
		require.Equal(t, shardCostForWrites, writes)
	})

	t.Run("bare negative uses defaults (no change)", func(t *testing.T) {
		t.Parallel()
		reads, writes, err := parseShardCostConfig("-1")
		require.NoError(t, err)
		require.Equal(t, shardCostForReads, reads)
		require.Equal(t, shardCostForWrites, writes)
	})

	t.Run("key=value sets both tables", func(t *testing.T) {
		t.Parallel()
		reads, writes, err := parseShardCostConfig("preferred=3.0,alternate=4.0")
		require.NoError(t, err)

		// preferred for reads = replica
		require.InDelta(t, 3.0, reads[shardCostReplica], 0)
		// alternate for reads = primary
		require.InDelta(t, 4.0, reads[shardCostPrimary], 0)
		// preferred for writes = primary
		require.InDelta(t, 3.0, writes[shardCostPrimary], 0)
		// alternate for writes = replica
		require.InDelta(t, 4.0, writes[shardCostReplica], 0)
	})

	t.Run("r: prefix with concrete key sets reads only", func(t *testing.T) {
		t.Parallel()
		reads, writes, err := parseShardCostConfig("r:replica=5.0")
		require.NoError(t, err)

		require.InDelta(t, 5.0, reads[shardCostReplica], 0)
		// Writes replica unchanged.
		require.InDelta(t, costAlternate, writes[shardCostReplica], 0)
	})

	t.Run("w: prefix with concrete key sets writes only", func(t *testing.T) {
		t.Parallel()
		reads, writes, err := parseShardCostConfig("w:primary=5.0")
		require.NoError(t, err)

		// Reads primary unchanged.
		require.InDelta(t, costAlternate, reads[shardCostPrimary], 0)
		require.InDelta(t, 5.0, writes[shardCostPrimary], 0)
	})

	t.Run("mixed concrete and abstract keys", func(t *testing.T) {
		t.Parallel()
		reads, writes, err := parseShardCostConfig("r:replica=1.0,w:primary=0.5,alternate=1")
		require.NoError(t, err)

		// r:replica=1.0 → reads[replica] = 1.0
		require.InDelta(t, 1.0, reads[shardCostReplica], 0)
		// w:primary=0.5 → writes[primary] = 0.5
		require.InDelta(t, 0.5, writes[shardCostPrimary], 0)
		// alternate=1 → reads[primary] = 1.0, writes[replica] = 1.0
		require.InDelta(t, 1.0, reads[shardCostPrimary], 0)
		require.InDelta(t, 1.0, writes[shardCostReplica], 0)
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
				name:   "abstract key rejected with prefix",
				spec:   "r:preferred=1.0",
				key:    "preferred",
				reason: "unknown key",
			},
			{
				name:   "concrete key rejected without prefix",
				spec:   "primary=1.0",
				key:    "primary",
				reason: "unknown key",
			},
			{
				name:   "unknown unprefixed key",
				spec:   "bogus=1.0",
				key:    "bogus",
				reason: "unknown key",
			},
			{
				name:   "unknown r: prefixed key",
				spec:   "r:bogus=1.0",
				key:    "bogus",
				reason: "unknown key",
			},
			{
				name:   "unknown w: prefixed key",
				spec:   "w:nope=1.0",
				key:    "nope",
				reason: "unknown key",
			},
			{
				name:   "unparseable float",
				spec:   "preferred=abc",
				key:    "preferred",
				reason: "invalid value",
				hasErr: true,
			},
			{
				name:   "missing value (no equals sign)",
				spec:   "preferred",
				key:    "preferred",
				reason: "missing value",
			},
			{
				name:   "missing value with prefix",
				spec:   "r:replica",
				key:    "replica",
				reason: "missing value",
			},
			{
				name:   "empty value after equals",
				spec:   "preferred=",
				key:    "preferred",
				reason: "invalid value",
				hasErr: true,
			},
			{
				name:   "valid key followed by invalid key",
				spec:   "preferred=1.0,bogus=2.0",
				key:    "bogus",
				reason: "unknown key",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				_, _, err := parseShardCostConfig(tt.spec)
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

	t.Run("all keys", func(t *testing.T) {
		t.Parallel()
		reads, writes, err := parseShardCostConfig("preferred=1.0,alternate=2.0,relocating=4.0,initializing=8.0,unknown=16.0")
		require.NoError(t, err)

		require.InDelta(t, 1.0, reads[shardCostReplica], 0)
		require.InDelta(t, 2.0, reads[shardCostPrimary], 0)
		require.InDelta(t, 4.0, reads[shardCostRelocating], 0)
		require.InDelta(t, 8.0, reads[shardCostInitializing], 0)
		require.InDelta(t, 16.0, reads[shardCostUnknown], 0)

		require.InDelta(t, 1.0, writes[shardCostPrimary], 0)
		require.InDelta(t, 2.0, writes[shardCostReplica], 0)
		require.InDelta(t, 4.0, writes[shardCostRelocating], 0)
		require.InDelta(t, 8.0, writes[shardCostInitializing], 0)
		require.InDelta(t, 16.0, writes[shardCostUnknown], 0)
	})

	t.Run("zero value in key=value falls back to default", func(t *testing.T) {
		t.Parallel()
		reads, writes, err := parseShardCostConfig("preferred=0,alternate=5.0")
		require.NoError(t, err)

		// preferred=0 → clamped to default
		require.InDelta(t, costPreferred, reads[shardCostReplica], 0)
		require.InDelta(t, costPreferred, writes[shardCostPrimary], 0)
		// alternate=5.0 → set
		require.InDelta(t, 5.0, reads[shardCostPrimary], 0)
		require.InDelta(t, 5.0, writes[shardCostReplica], 0)
	})

	t.Run("negative value in key=value falls back to default", func(t *testing.T) {
		t.Parallel()
		reads, writes, err := parseShardCostConfig("unknown=-1.0")
		require.NoError(t, err)

		require.InDelta(t, costUnknown, reads[shardCostUnknown], 0)
		require.InDelta(t, costUnknown, writes[shardCostUnknown], 0)
	})

	t.Run("trailing comma is ignored", func(t *testing.T) {
		t.Parallel()
		reads, _, err := parseShardCostConfig("preferred=3.0,")
		require.NoError(t, err)
		require.InDelta(t, 3.0, reads[shardCostReplica], 0)
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
				err:  ShardCostConfigError{Key: "preferred", Reason: "unknown key"},
				want: `shard cost config: unknown key for key "preferred"`,
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
					require.Nil(t, errors.Unwrap(&tt.err))
				} else {
					require.ErrorIs(t, &tt.err, tt.target)
				}
			})
		}
	})
}

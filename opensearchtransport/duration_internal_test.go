// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDurationFromStd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    time.Duration
		want durationNanos
	}{
		{"zero", 0, 0},
		{"1ns", time.Nanosecond, 1},
		{"1us", time.Microsecond, 1000},
		{"1ms", time.Millisecond, 1_000_000},
		{"1s", time.Second, 1_000_000_000},
		{"negative", -5 * time.Millisecond, -5_000_000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := durationFromStd(tt.d)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestDurationNanosMicros(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		nanos durationNanos
		want  durationMicros
	}{
		{"zero", 0, 0},
		{"sub-microsecond truncates", 999, 0},
		{"exact 1us", 1000, 1},
		{"1500ns truncates to 1us", 1500, 1},
		{"1ms", 1_000_000, 1000},
		{"negative", -5_000_000, -5000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.nanos.Micros()
			require.Equal(t, tt.want, got)
		})
	}
}

func TestDurationMicrosBucket(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		micros durationMicros
		want   rttBucket
	}{
		{"zero returns unknown", 0, rttBucketUnknown},
		{"negative returns unknown", -100, rttBucketUnknown},
		{"1us = bucket 8 (floor)", 1, rttBucketFloor},
		{"255us = bucket 8 (floor)", 255, rttBucketFloor},
		{"256us = bucket 8", 256, 8},
		{"512us = bucket 9", 512, 9},
		{"1000us = bucket 9", 1000, 9},
		{"3000us = bucket 11", 3000, 11},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.micros.Bucket()
			require.Equal(t, tt.want, got)
		})
	}
}

func TestRTTBucketIsUnknown(t *testing.T) {
	t.Parallel()

	require.True(t, rttBucketUnknown.IsUnknown())
	require.False(t, rttBucket(1).IsUnknown())
	require.False(t, rttBucket(0).IsUnknown())
}

func TestRTTBucketMicrosRoundTrip(t *testing.T) {
	t.Parallel()

	// Bucket -> Micros -> Bucket should be identity for exact
	// powers of two at or above the floor (256us).
	for _, us := range []durationMicros{256, 512, 1024, 65536} {
		bucket := us.Bucket()
		require.False(t, bucket.IsUnknown())
		reconstructed := bucket.Micros().Bucket()
		require.Equal(t, bucket, reconstructed, "round-trip failed for %d us", us)
	}
}

func TestDurationNanosRoundTrip(t *testing.T) {
	t.Parallel()

	// time.Duration -> durationNanos -> time.Duration should be identity.
	for _, d := range []time.Duration{0, time.Nanosecond, time.Millisecond, 42 * time.Second} {
		require.Equal(t, d, durationFromStd(d).Duration())
	}
}

func TestDurationMicrosRoundTrip(t *testing.T) {
	t.Parallel()

	// durationMicros -> durationNanos -> durationMicros should be identity.
	for _, m := range []durationMicros{0, 1, 1000, 1_000_000} {
		require.Equal(t, m, m.Nanos().Micros())
	}
}

func TestDurationNanosIsPositive(t *testing.T) {
	t.Parallel()

	require.False(t, durationNanos(0).IsPositive())
	require.False(t, durationNanos(-1).IsPositive())
	require.True(t, durationNanos(1).IsPositive())
}

func TestDurationMicrosIsPositive(t *testing.T) {
	t.Parallel()

	require.False(t, durationMicros(0).IsPositive())
	require.False(t, durationMicros(-1).IsPositive())
	require.True(t, durationMicros(1).IsPositive())
}

func TestRTTBucketInt64(t *testing.T) {
	t.Parallel()

	require.Equal(t, int64(42), rttBucket(42).Int64())
	require.Equal(t, int64(rttBucketUnknown), rttBucketUnknown.Int64())
}

func TestRTTBucketFromInt64(t *testing.T) {
	t.Parallel()

	require.Equal(t, rttBucket(42), rttBucketFromInt64(42))
	require.Equal(t, rttBucketUnknown, rttBucketFromInt64(rttBucketUnknown.Int64()))
}

func TestEndToEndConversionChain(t *testing.T) {
	t.Parallel()

	// Simulate what recordCPUTime does:
	// time.Duration -> durationNanos -> divide by processors -> Micros -> float64 for accumulator.
	requestDuration := 10 * time.Millisecond // 10ms total
	baseline := 1 * time.Millisecond         // 1ms RTT

	serverTime := durationFromStd(requestDuration - baseline) // 9ms = 9,000,000 ns
	require.Equal(t, durationNanos(9_000_000), serverTime)
	require.True(t, serverTime.IsPositive())

	processors := 4
	cpuNanos := durationNanos(int64(serverTime) / int64(processors)) // 2,250,000 ns
	require.Equal(t, durationNanos(2_250_000), cpuNanos)

	cpuMicros := cpuNanos.Micros() // 2250 us
	require.Equal(t, durationMicros(2250), cpuMicros)

	// The float64 value added to the accumulator should be 2250.0
	require.InDelta(t, 2250.0, float64(cpuMicros), 0)
}

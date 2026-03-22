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

func TestRTTBucketOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		d        time.Duration
		expected rttBucket
	}{
		{"zero returns unknown", 0, rttBucketUnknown},
		{"negative returns unknown", -1 * time.Millisecond, rttBucketUnknown},
		{"1us = floor", 1 * time.Microsecond, rttBucketFloor},
		{"255us = floor", 255 * time.Microsecond, rttBucketFloor},
		{"256us = bucket 8", 256 * time.Microsecond, 8},
		{"512us = bucket 9", 512 * time.Microsecond, 9},
		{"1ms = bucket 9", 1 * time.Millisecond, 9},
		{"3ms = bucket 11", 3 * time.Millisecond, 11},
		{"100ms = bucket 16", 100 * time.Millisecond, 16},
		{"40s = bucket 25", 40 * time.Second, 25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := rttBucketOf(tt.d)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestNewRTTRing(t *testing.T) {
	t.Parallel()

	t.Run("default size clamps to 1", func(t *testing.T) {
		t.Parallel()
		r := newRTTRing(0)
		require.Len(t, r.buckets, 1)
		require.Equal(t, rttBucketUnknown.Int64(), r.buckets[0].Load())
	})

	t.Run("negative size clamps to 1", func(t *testing.T) {
		t.Parallel()
		r := newRTTRing(-5)
		require.Len(t, r.buckets, 1)
	})

	t.Run("all slots init to unknown", func(t *testing.T) {
		t.Parallel()
		r := newRTTRing(defaultRTTRingSize)
		require.Len(t, r.buckets, defaultRTTRingSize)
		for i := range r.buckets {
			require.Equal(t, rttBucketUnknown.Int64(), r.buckets[i].Load(), "slot %d should be unknown", i)
		}
	})
}

func TestRTTRingMedianBucket(t *testing.T) {
	t.Parallel()

	t.Run("nil ring returns unknown", func(t *testing.T) {
		t.Parallel()
		var r *rttRing
		require.Equal(t, rttBucketUnknown, r.medianBucket())
	})

	t.Run("empty ring returns unknown", func(t *testing.T) {
		t.Parallel()
		r := newRTTRing(defaultRTTRingSize)
		require.Equal(t, rttBucketUnknown, r.medianBucket())
	})

	t.Run("partially-filled ring uses only measured values", func(t *testing.T) {
		t.Parallel()
		// 12 slots, fill 5. Median is computed over just the 5 measured
		// values, not the full ring.
		r := newRTTRing(defaultRTTRingSize)
		for range 5 {
			r.add(1 * time.Millisecond) // bucket ~3
		}
		median := r.medianBucket()
		require.NotEqual(t, rttBucketUnknown, median)
		require.Equal(t, rttBucketOf(1*time.Millisecond), median)
	})

	t.Run("majority-filled ring returns measured bucket", func(t *testing.T) {
		t.Parallel()
		r := newRTTRing(defaultRTTRingSize)
		// Fill 7 of 12 slots with 1ms (bucket=3). Median at index 6 of sorted
		// array should be the measured value.
		for range 7 {
			r.add(1 * time.Millisecond)
		}
		median := r.medianBucket()
		require.NotEqual(t, rttBucketUnknown, median)
		require.Equal(t, rttBucketOf(1*time.Millisecond), median)
	})

	t.Run("ring wraps around correctly", func(t *testing.T) {
		t.Parallel()
		r := newRTTRing(4)
		// Write 6 entries into a ring of 4 -- wraps around.
		for range 6 {
			r.add(2 * time.Millisecond) // bucket ~7
		}
		median := r.medianBucket()
		require.Equal(t, rttBucketOf(2*time.Millisecond), median)
	})
}

func TestRTTRingAdd(t *testing.T) {
	t.Parallel()

	t.Run("nil ring does not panic", func(t *testing.T) {
		t.Parallel()
		var r *rttRing
		require.NotPanics(t, func() { r.add(1 * time.Millisecond) })
	})

	t.Run("overwrites oldest slot", func(t *testing.T) {
		t.Parallel()
		r := newRTTRing(3)
		r.add(1 * time.Millisecond)
		r.add(2 * time.Millisecond)
		r.add(3 * time.Millisecond)
		// All 3 slots filled with different values.
		r.add(10 * time.Millisecond) // overwrites slot 0

		// Slot 0 should now have the 10ms bucket.
		require.Equal(t, rttBucketOf(10*time.Millisecond).Int64(), r.buckets[0].Load())
		// Slot 1 should still have 2ms.
		require.Equal(t, rttBucketOf(2*time.Millisecond).Int64(), r.buckets[1].Load())
	})
}

func TestRTTRingMedianTransition(t *testing.T) {
	t.Parallel()

	// Verify that the median uses only measured values: a single health
	// check is enough for a real median (no warmup gate).
	r := newRTTRing(10)

	// Before any writes, median is unknown.
	require.Equal(t, rttBucketUnknown, r.medianBucket())

	// After the first write, median is the measured value.
	r.add(500 * time.Microsecond) // bucket=1
	require.Equal(t, rttBucketOf(500*time.Microsecond), r.medianBucket(),
		"single sample should produce a real median")

	// After filling half the ring with a different value, median reflects
	// the available data (3 values of 2ms, 1 value of 500us -> sorted
	// [1,7,7,7] -> median at index 2 = 7).
	for range 3 {
		r.add(2 * time.Millisecond) // bucket ~7
	}
	require.Equal(t, rttBucketOf(2*time.Millisecond), r.medianBucket(),
		"after 4 samples, median should reflect majority value")

	// Fill all 10 slots.
	for range 6 {
		r.add(500 * time.Microsecond) // bucket=1
	}
	require.Equal(t, rttBucketOf(500*time.Microsecond), r.medianBucket(),
		"after 10 samples in ring of 10, median reflects all values")
}

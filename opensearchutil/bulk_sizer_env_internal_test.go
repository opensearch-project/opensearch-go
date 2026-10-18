// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
)

// These tests set environment variables, so none of them runs in parallel.

func TestBulkSizingEnvMode(t *testing.T) {
	tests := []struct {
		name      string
		fixed     bool
		env       string
		wantFixed bool
	}{
		{name: "unset keeps adaptive", fixed: false, wantFixed: false},
		{name: "unset keeps fixed", fixed: true, wantFixed: true},
		{name: "fixed overrides adaptive", fixed: false, env: "fixed", wantFixed: true},
		{name: "adaptive overrides fixed", fixed: true, env: "adaptive", wantFixed: false},
		{name: "case and space are ignored", fixed: false, env: "  FIXED ", wantFixed: true},
		{name: "an unknown mode is ignored", fixed: true, env: "turbo", wantFixed: true},
		{name: "an empty value is ignored", fixed: true, env: "", wantFixed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envvars.BulkMode, tt.env)

			r := BulkSizing{Fixed: tt.fixed}.resolve(testCeiling)
			require.Equal(t, tt.wantFixed, r.fixed)
			if tt.wantFixed {
				require.InDelta(t, testCeiling, r.initial, 0, "a fixed size starts at the ceiling")
				require.InDelta(t, testCeiling, r.min, 0)
			}
		})
	}
}

func TestBulkSizingEnvAIMD(t *testing.T) {
	tests := []struct {
		name     string
		aimd     bool
		env      string
		wantAIMD bool
	}{
		{name: "unset keeps off", aimd: false, wantAIMD: false},
		{name: "unset keeps on", aimd: true, wantAIMD: true},
		{name: "on overrides off", aimd: false, env: "on", wantAIMD: true},
		{name: "off overrides on", aimd: true, env: "off", wantAIMD: false},
		{name: "case is ignored", aimd: false, env: "ON", wantAIMD: true},
		{name: "true is accepted", aimd: false, env: "true", wantAIMD: true},
		{name: "0 is accepted", aimd: true, env: "0", wantAIMD: false},
		{name: "garbage is ignored", aimd: true, env: "maybe", wantAIMD: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envvars.BulkAIMD, tt.env)

			require.Equal(t, tt.wantAIMD, BulkSizing{AIMD: tt.aimd}.resolve(testCeiling).aimd)
		})
	}
}

func TestBulkSizingEnvPIDGains(t *testing.T) {
	t.Run("each gain overrides on its own", func(t *testing.T) {
		t.Setenv(envvars.BulkPIDKi, "0.5")

		r := BulkSizing{}.resolve(testCeiling)
		require.Equal(t, [3]float64{sizingDefaultKp, 0.5, sizingDefaultKd}, [3]float64{r.kp, r.ki, r.kd})
	})

	t.Run("they override programmatic gains", func(t *testing.T) {
		t.Setenv(envvars.BulkPIDKp, "3")
		t.Setenv(envvars.BulkPIDKi, "4")
		t.Setenv(envvars.BulkPIDKd, "5")

		r := BulkSizing{Kp: 1, Ki: 1, Kd: 1}.resolve(testCeiling)
		require.Equal(t, [3]float64{3, 4, 5}, [3]float64{r.kp, r.ki, r.kd})
	})

	t.Run("zero turns a term off", func(t *testing.T) {
		t.Setenv(envvars.BulkPIDKi, "0")
		t.Setenv(envvars.BulkPIDKd, "0")

		r := BulkSizing{}.resolve(testCeiling)
		require.Equal(t, [3]float64{sizingDefaultKp, 0, 0}, [3]float64{r.kp, r.ki, r.kd})
	})

	for _, bad := range []string{"abc", "-1", "NaN", "Inf", "+Inf", ""} {
		t.Run("ignores "+bad, func(t *testing.T) {
			t.Setenv(envvars.BulkPIDKp, bad)

			require.InDelta(t, sizingDefaultKp, BulkSizing{}.resolve(testCeiling).kp, 0)
		})
	}
}

func TestSizerAIMDGrowsByAFixedStep(t *testing.T) {
	// Gains this large would dominate a PID step; AIMD must ignore them.
	s := newTestSizer(BulkSizing{AIMD: true, Kp: 100, Ki: 100, Kd: 100})
	step := int64(s.cfg.step)

	for range 5 {
		before := s.bytes()
		s.observe(flushOf(s.bytes(), 100e6))
		require.Equal(t, before+step, s.bytes(), "a healthy flush adds exactly StepBytes")
	}
}

func TestSizerAIMDCutsByAFixedFactor(t *testing.T) {
	s := newTestSizer(BulkSizing{AIMD: true})
	for range 40 {
		s.observe(flushOf(s.bytes(), 100e6))
	}
	healthy := s.bytes()
	require.Greater(t, healthy, int64(s.cfg.initial))

	// Five times slower than the best rate seen, so far past the headroom.
	s.observe(flushOf(s.bytes(), 20e6))
	want := float64(healthy) * math.Sqrt(s.cfg.backoff)
	require.InDelta(t, want, float64(s.bytes()), 1, "a slow flush multiplies the size by sqrt(Backoff)")

	// A cut that is not a rejection starts no hold, so the next healthy flush grows it.
	before := s.bytes()
	s.observe(flushOf(s.bytes(), 100e6))
	require.Greater(t, s.bytes(), before)
}

func TestSizerAIMDHoldsOnAnOverloadedPool(t *testing.T) {
	s := newTestSizer(BulkSizing{AIMD: true})
	for range 10 {
		s.observe(flushOf(s.bytes(), 100e6))
	}
	before := s.bytes()

	// The node keeps up, but the transport sees its write pool rejecting.
	for range 5 {
		f := flushOf(s.bytes(), 100e6)
		f.overloaded = true
		s.observe(f)
	}
	require.Equal(t, before, s.bytes(), "an overloaded pool neither grows nor cuts the size")
}

func TestSizerAIMDHoldsAfterARejection(t *testing.T) {
	s := newTestSizer(BulkSizing{AIMD: true})
	for range 40 {
		s.observe(flushOf(s.bytes(), 100e6))
	}

	s.observe(rejectedFlush())
	cut := s.bytes()
	for range sizingHoldFlushes {
		s.observe(flushOf(s.bytes(), 100e6))
		require.Equal(t, cut, s.bytes(), "no growth while the hold lasts")
	}
	s.observe(flushOf(s.bytes(), 100e6))
	require.Greater(t, s.bytes(), cut, "growth resumes after the hold")
}

func TestBulkIndexerEnvFixedModeFixesTheNodeLimit(t *testing.T) {
	t.Setenv(envvars.BulkMode, "fixed")

	bi := newSizingHarness(t, 400, 1, "n0").bi.(*bulkIndexer)

	require.True(t, bi.sizing.fixed)
	require.True(t, bi.nodeSizing.fixed)
	require.Less(t, bi.config.NodeBytes, bi.config.MaxMemoryBytes, "the share must differ from the whole job for this test to tell")
	share := float64(bi.config.NodeBytes)
	require.Equal(t, [3]float64{share, share, share},
		[3]float64{bi.nodeSizing.min, bi.nodeSizing.initial, bi.nodeSizing.max},
		"a fixed node limit is the even share, not the whole job")
}

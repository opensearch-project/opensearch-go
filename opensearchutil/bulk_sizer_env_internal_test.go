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
	"time"

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
			sizing: BulkSizing{Kp: 1, Ki: 1, Kd: 1},
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
			for name, value := range tt.env {
				t.Setenv(name, value)
			}

			r := tt.sizing.resolve(testCeiling)
			want, got := tt.want, [3]float64{r.kp, r.ki, r.kd}
			require.InDeltaSlice(t, want[:], got[:], 1e-9, "kp, ki, kd")
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

func TestBulkIndexerByteWindowStart(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		// nodeBytes is NodeBytes in the config, 0 for unset.
		nodeBytes int
		// wantCeiling is where the window starts and the most it grows to, wantFixed
		// whether it can move.
		wantCeiling float64
		wantFixed   bool
	}{
		{name: "an unset NodeBytes leaves the window as wide as the job", wantCeiling: 64 << 20},
		{name: "NodeBytes caps the window", nodeBytes: 8 << 20, wantCeiling: 8 << 20},
		{name: "NodeBytes is held to the job", nodeBytes: 1 << 40, wantCeiling: 64 << 20},
		{
			name: "fixed mode holds the window where it started", env: map[string]string{envvars.BulkMode: "fixed"},
			nodeBytes: 8 << 20, wantCeiling: 8 << 20, wantFixed: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for name, value := range tt.env {
				t.Setenv(name, value)
			}

			cluster := newFakeCluster(map[string]ShardMap{"idx": DefaultShardMap(2)}, "n0", "n1")
			bi := newClusterIndexer(t, newFakeServer(cluster), BulkIndexerConfig{
				NumWorkers: 1, FlushBytes: 1 << 20, MaxMemoryBytes: 64 << 20, NodeBytes: tt.nodeBytes, FlushInterval: time.Hour,
			}).(*bulkIndexer)

			require.InDelta(t, tt.wantCeiling, bi.window.start, 0, "it starts wide open")
			require.InDelta(t, tt.wantCeiling, bi.window.ceiling, 0)
			require.Equal(t, tt.wantFixed, bi.window.fixed)
		})
	}
}

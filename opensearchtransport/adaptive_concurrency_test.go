// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport //nolint:testpackage // tests unexported computeAdaptiveConcurrency, appendAdaptiveConcurrency, etc.

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComputeAdaptiveConcurrency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cwnd     int32
		cfg      adaptiveConcurrencyConfig
		features routingFeatures
		want     int
	}{
		{
			name: "default cwnd",
			cwnd: 20, want: 20,
		},
		{
			name: "low cwnd clamped to min",
			cwnd: 3, want: 5,
		},
		{
			name: "high cwnd clamped to max",
			cwnd: 500, want: 256,
		},
		{
			name: "cwnd=1 overloaded clamped to min",
			cwnd: 1, want: 5,
		},
		{
			name:     "feature disabled returns 0",
			cwnd:     20,
			features: routingSkipAdaptiveConcurrency,
			want:     0,
		},
		{
			name: "custom min",
			cwnd: 3,
			cfg:  adaptiveConcurrencyConfig{minVal: 10},
			want: 10,
		},
		{
			name: "custom max allows higher",
			cwnd: 400,
			cfg:  adaptiveConcurrencyConfig{maxVal: 512},
			want: 400,
		},
		{
			name: "custom max clamps",
			cwnd: 600,
			cfg:  adaptiveConcurrencyConfig{maxVal: 512},
			want: 512,
		},
		{
			name: "custom min and max",
			cwnd: 200,
			cfg:  adaptiveConcurrencyConfig{minVal: 10, maxVal: 512},
			want: 200,
		},
		{
			name: "exactly at min",
			cwnd: 5, want: 5,
		},
		{
			name: "exactly at max",
			cwnd: 256, want: 256,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := computeAdaptiveConcurrency(tt.cwnd, tt.cfg, tt.features)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestAppendAdaptiveConcurrency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     string
		rawQuery string
		mcsr     int
		want     string
	}{
		{
			name: "empty query",
			path: "/idx/_search",
			mcsr: 42,
			want: "max_concurrent_shard_requests=42",
		},
		{
			name:     "existing params",
			path:     "/idx/_search",
			rawQuery: "pretty=true",
			mcsr:     10,
			want:     "pretty=true&max_concurrent_shard_requests=10",
		},
		{
			name:     "caller override preserved",
			path:     "/idx/_search",
			rawQuery: "max_concurrent_shard_requests=3&pretty=true",
			mcsr:     42,
			want:     "max_concurrent_shard_requests=3&pretty=true",
		},
		{
			name:     "caller override in middle preserved",
			path:     "/idx/_search",
			rawQuery: "pretty=true&max_concurrent_shard_requests=7&timeout=30s",
			mcsr:     42,
			want:     "pretty=true&max_concurrent_shard_requests=7&timeout=30s",
		},
		{
			name:     "similar param name not treated as override",
			path:     "/idx/_search",
			rawQuery: "not_max_concurrent_shard_requests=5",
			mcsr:     42,
			want:     "not_max_concurrent_shard_requests=5&max_concurrent_shard_requests=42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := &http.Request{URL: &url.URL{Path: tt.path, RawQuery: tt.rawQuery}}
			appendAdaptiveConcurrency(req, tt.mcsr)
			require.Equal(t, tt.want, req.URL.RawQuery)
		})
	}
}

func TestParseShardRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		value        string
		inFeatures   routingFeatures
		wantCfg      adaptiveConcurrencyConfig
		wantEnabled  *bool // nil = don't check
		wantMinEff   int   // 0 = don't check effectiveMin
		wantMaxEff   int   // 0 = don't check effectiveMax
		wantFeatures *routingFeatures
	}{
		{
			name:         "empty string",
			value:        "",
			wantCfg:      adaptiveConcurrencyConfig{},
			wantFeatures: ptr(routingFeatures(0)),
		},
		{
			name:        "true re-enables",
			value:       "true",
			inFeatures:  routingSkipAdaptiveConcurrency,
			wantCfg:     adaptiveConcurrencyConfig{},
			wantEnabled: ptr(true),
		},
		{
			name:        "false disables",
			value:       "false",
			wantCfg:     adaptiveConcurrencyConfig{},
			wantEnabled: ptr(false),
		},
		{
			name:        "zero disables (ParseBool)",
			value:       "0",
			wantEnabled: ptr(false),
		},
		{
			name:        "one enables (ParseBool)",
			value:       "1",
			inFeatures:  routingSkipAdaptiveConcurrency,
			wantEnabled: ptr(true),
		},
		{
			name:        "min:max",
			value:       "10:512",
			inFeatures:  routingSkipAdaptiveConcurrency,
			wantCfg:     adaptiveConcurrencyConfig{minVal: 10, maxVal: 512},
			wantEnabled: ptr(true),
		},
		{
			name:        "min only",
			value:       "10:",
			wantCfg:     adaptiveConcurrencyConfig{minVal: 10},
			wantMaxEff:  adaptiveConcurrencyMaxDefault,
			wantEnabled: ptr(true),
		},
		{
			name:        "max only",
			value:       ":512",
			wantCfg:     adaptiveConcurrencyConfig{maxVal: 512},
			wantMinEff:  adaptiveConcurrencyMinDefault,
			wantEnabled: ptr(true),
		},
		{
			name:        "bare integer as min",
			value:       "10",
			wantCfg:     adaptiveConcurrencyConfig{minVal: 10},
			wantEnabled: ptr(true),
		},
		{
			name:         "garbage ignored",
			value:        "notanumber",
			wantCfg:      adaptiveConcurrencyConfig{},
			wantFeatures: ptr(routingFeatures(0)),
		},
		{
			name:        "whitespace trimmed",
			value:       "  10:512  ",
			wantCfg:     adaptiveConcurrencyConfig{minVal: 10, maxVal: 512},
			wantEnabled: ptr(true),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, features := parseShardRequests(tt.value, tt.inFeatures)
			require.Equal(t, tt.wantCfg, cfg)
			if tt.wantEnabled != nil {
				require.Equal(t, *tt.wantEnabled, features.adaptiveConcurrencyEnabled())
			}
			if tt.wantFeatures != nil {
				require.Equal(t, *tt.wantFeatures, features)
			}
			if tt.wantMinEff > 0 {
				require.Equal(t, tt.wantMinEff, cfg.effectiveMin())
			}
			if tt.wantMaxEff > 0 {
				require.Equal(t, tt.wantMaxEff, cfg.effectiveMax())
			}
		})
	}
}

func TestAdaptiveConcurrencyConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     adaptiveConcurrencyConfig
		wantMin int
		wantMax int
	}{
		{
			name:    "zero-value defaults",
			wantMin: adaptiveConcurrencyMinDefault,
			wantMax: adaptiveConcurrencyMaxDefault,
		},
		{
			name:    "custom override",
			cfg:     adaptiveConcurrencyConfig{minVal: 10, maxVal: 512},
			wantMin: 10,
			wantMax: 512,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.wantMin, tt.cfg.effectiveMin())
			require.Equal(t, tt.wantMax, tt.cfg.effectiveMax())
		})
	}
}

func TestRoutingFeatures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		features           routingFeatures
		wantAdaptive       bool
		wantShardExact     bool
		checkShardExact    bool
		checkAdaptiveIndep bool // check that the other bit is independent
	}{
		{
			name:         "zero-value enables adaptive concurrency",
			wantAdaptive: true,
		},
		{
			name:         "skip flag disables adaptive concurrency",
			features:     routingSkipAdaptiveConcurrency,
			wantAdaptive: false,
		},
		{
			name:               "shard_exact skip does not affect adaptive concurrency",
			features:           routingSkipShardExact,
			wantAdaptive:       true,
			wantShardExact:     false,
			checkShardExact:    true,
			checkAdaptiveIndep: true,
		},
		{
			name:            "adaptive skip does not affect shard_exact",
			features:        routingSkipAdaptiveConcurrency,
			wantAdaptive:    false,
			wantShardExact:  true,
			checkShardExact: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.wantAdaptive, tt.features.adaptiveConcurrencyEnabled())
			if tt.checkShardExact {
				require.Equal(t, tt.wantShardExact, tt.features.shardExactEnabled())
			}
		})
	}
}

func TestWithAdaptiveConcurrencyLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		min, max    int
		startSkip   bool // start with adaptive concurrency disabled
		wantEnabled bool
		wantMin     int
		wantMax     int
		wantErrs    int
	}{
		{
			name:        "zero enables with defaults",
			startSkip:   true,
			wantEnabled: true,
		},
		{
			name:        "negative disables",
			min:         -1,
			max:         -1,
			wantEnabled: false,
		},
		{
			name:        "positive values",
			min:         10,
			max:         512,
			startSkip:   true,
			wantEnabled: true,
			wantMin:     10,
			wantMax:     512,
		},
		{
			name:        "min exceeds max records error",
			min:         512,
			max:         10,
			wantEnabled: true,
			wantMin:     512,
			wantMax:     10,
			wantErrs:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := routerConfig{}
			if tt.startSkip {
				cfg.routingFeatures |= routingSkipAdaptiveConcurrency
			}
			WithAdaptiveConcurrencyLimits(tt.min, tt.max)(&cfg)
			require.Equal(t, tt.wantEnabled, cfg.routingFeatures.adaptiveConcurrencyEnabled())
			require.Equal(t, tt.wantMin, cfg.adaptiveConcurrency.minVal)
			require.Equal(t, tt.wantMax, cfg.adaptiveConcurrency.maxVal)
			require.Len(t, cfg.errs, tt.wantErrs)
		})
	}
}

func TestWithAdaptiveConcurrency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		enable    bool
		startSkip bool
		want      bool
	}{
		{
			name:      "enable",
			enable:    true,
			startSkip: true,
			want:      true,
		},
		{
			name: "disable",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := routerConfig{}
			if tt.startSkip {
				cfg.routingFeatures |= routingSkipAdaptiveConcurrency
			}
			WithAdaptiveConcurrency(tt.enable)(&cfg)
			require.Equal(t, tt.want, cfg.routingFeatures.adaptiveConcurrencyEnabled())
		})
	}
}

func TestParseRoutingConfig_AdaptiveConcurrency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		value           string
		wantAdaptive    bool
		wantShardExact  bool
		checkShardExact bool
	}{
		{
			name:            "disable adaptive_mcsr",
			value:           "-adaptive_mcsr",
			wantAdaptive:    false,
			wantShardExact:  true,
			checkShardExact: true,
		},
		{
			name:         "re-enable adaptive_mcsr",
			value:        "-adaptive_mcsr,+adaptive_mcsr",
			wantAdaptive: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			features := parseRoutingConfig(tt.value)
			require.Equal(t, tt.wantAdaptive, features.adaptiveConcurrencyEnabled())
			if tt.checkShardExact {
				require.Equal(t, tt.wantShardExact, features.shardExactEnabled())
			}
		})
	}
}

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
)

// The heap, CPU and IO overload thresholds resolve the same way: an
// environment value that parses wins over Config, and 0 means the default.
func TestOverloadThresholdsResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                      string
		cfg                       Config
		env                       map[string]string
		wantHeap, wantCPU, wantIO int
	}{
		{name: "defaults", wantHeap: defaultOverloadedHeapThreshold, wantCPU: defaultOverloadedCPUPercent, wantIO: defaultOverloadedIOPercent},
		{
			name:     "config values",
			cfg:      Config{OverloadedHeapThreshold: 70, OverloadedCPUPercent: 80, OverloadedIOPercent: 90},
			wantHeap: 70, wantCPU: 80, wantIO: 90,
		},
		{
			name:     "environment wins over config",
			cfg:      Config{OverloadedCPUPercent: 80, OverloadedIOPercent: 90},
			env:      map[string]string{envvars.OverloadedCPUPercent: "60", envvars.OverloadedIOPercent: "101"},
			wantHeap: defaultOverloadedHeapThreshold, wantCPU: 60, wantIO: 101,
		},
		{
			name:     "an unparseable environment value keeps config",
			cfg:      Config{OverloadedCPUPercent: 80},
			env:      map[string]string{envvars.OverloadedCPUPercent: "high"},
			wantHeap: defaultOverloadedHeapThreshold, wantCPU: 80, wantIO: defaultOverloadedIOPercent,
		},
		{
			name:     "an environment 0 means the default",
			cfg:      Config{OverloadedIOPercent: 90},
			env:      map[string]string{envvars.OverloadedIOPercent: "0"},
			wantHeap: defaultOverloadedHeapThreshold, wantCPU: defaultOverloadedCPUPercent, wantIO: defaultOverloadedIOPercent,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := tt.cfg
			cfg.URLs = []*url.URL{{Scheme: "http", Host: "localhost:9200"}}
			cfg.lookupEnv = envLookup(tt.env)
			client, err := New(cfg)
			require.NoError(t, err)
			defer client.Close()

			require.Equal(t, tt.wantHeap, client.overloadedHeapThreshold)
			require.Equal(t, tt.wantCPU, client.overloadedCPUPercent)
			require.Equal(t, tt.wantIO, client.overloadedIOPercent)
		})
	}
}

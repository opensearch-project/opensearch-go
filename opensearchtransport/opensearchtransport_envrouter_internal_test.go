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
)

// TestEnvRouterAutoConstruction verifies that OPENSEARCH_GO_ROUTER triggers
// auto-construction of the DefaultRouter when no programmatic Config.Router
// is provided, honors precedence rules, and surfaces parse errors from
// ShardCostConfig.
func TestEnvRouterAutoConstruction(t *testing.T) {
	tests := []struct {
		name            string
		envValue        string // empty value is treated the same as unset by envvars.Truthy
		shardCostConfig string
		programmaticSet bool // when true, test uses a non-nil Config.Router

		wantRouter bool   // expect client.router != nil
		wantErr    bool   // expect New() to return error
		errSubstr  string // substring expected in error message
	}{
		{
			name:       "env empty, no programmatic router",
			envValue:   "",
			wantRouter: false,
		},
		{
			name:       "env=true triggers auto-construction",
			envValue:   "true",
			wantRouter: true,
		},
		{
			name:       "env=1 triggers auto-construction",
			envValue:   "1",
			wantRouter: true,
		},
		{
			name:       "env=false leaves router nil",
			envValue:   "false",
			wantRouter: false,
		},
		{
			name:       "env=garbage leaves router nil",
			envValue:   "yes-please",
			wantRouter: false,
		},
		{
			name:            "env=true with valid ShardCostConfig",
			envValue:        "true",
			shardCostConfig: "r:base=0.9,r:amplify=2.5",
			wantRouter:      true,
		},
		{
			name:            "env=true with invalid ShardCostConfig returns error",
			envValue:        "true",
			shardCostConfig: "bogus=1.0",
			wantErr:         true,
			errSubstr:       "shard cost config",
		},
		{
			name:            "programmatic router takes precedence over env",
			envValue:        "true",
			programmaticSet: true,
			wantRouter:      true,
		},
		{
			name:            "programmatic router used; env unset; ShardCostConfig ignored",
			envValue:        "",
			programmaticSet: true,
			shardCostConfig: "r:base=0.9",
			wantRouter:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Cannot use t.Parallel(): t.Setenv mutates process state.
			t.Setenv(envRouter, tt.envValue)

			cfg := Config{
				URLs:            []*url.URL{{Scheme: "http", Host: "localhost:9200"}},
				ShardCostConfig: tt.shardCostConfig,
			}
			if tt.programmaticSet {
				router, err := NewDefaultRouter()
				require.NoError(t, err)
				cfg.Router = router
			}

			client, err := New(cfg)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errSubstr)
				return
			}
			require.NoError(t, err)
			defer client.Close()

			if tt.wantRouter {
				require.NotNil(t, client.router, "expected router to be set")
			} else {
				require.Nil(t, client.router, "expected router to be nil")
			}
		})
	}
}

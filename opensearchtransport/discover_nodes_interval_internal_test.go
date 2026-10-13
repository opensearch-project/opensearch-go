// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestDiscoverNodesIntervalDefault checks how New resolves
// Config.DiscoverNodesInterval. The router-less cases rely on TestMain
// setting OPENSEARCH_GO_ROUTER=false, so only a programmatic Router enables
// routing here.
func TestDiscoverNodesIntervalDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		withRouter bool
		interval   time.Duration
		want       time.Duration
	}{
		{name: "router, unset uses default", withRouter: true, interval: 0, want: defaultDiscoverNodesInterval},
		{name: "router, negative disables", withRouter: true, interval: -1, want: 0},
		{name: "router, explicit kept", withRouter: true, interval: 5 * time.Minute, want: 5 * time.Minute},
		{name: "no router, unset stays disabled", interval: 0, want: 0},
		{name: "no router, negative disables", interval: -1, want: 0},
		{name: "no router, explicit kept", interval: 5 * time.Minute, want: 5 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := Config{
				URLs:                  []*url.URL{{Scheme: "http", Host: "localhost:9200"}},
				DiscoverNodesInterval: tt.interval,
			}
			if tt.withRouter {
				router, err := NewDefaultRouter()
				require.NoError(t, err)
				cfg.Router = router
			}

			client, err := New(cfg)
			require.NoError(t, err)
			t.Cleanup(func() { client.Close() })

			require.Equal(t, tt.want, client.discoverNodesInterval)
			require.Equal(t, rttRingSizeFor(tt.want, client.resurrectTimeoutInitial), client.rttRingSize)
		})
	}
}

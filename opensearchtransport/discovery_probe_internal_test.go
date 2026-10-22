// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

// TestProbeUnprovenConnection covers the background health check a
// singleServerPool's node gets, since the pool has no resurrection of its own.
func TestProbeUnprovenConnection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// conn builds the connection to probe; seed and an already running
		// check are set here.
		conn func(u *url.URL) *Connection
		// answers is set when the node answers its health check.
		answers      bool
		wantProbed   bool
		wantViable   bool
		wantChecking bool
	}{
		{
			name:       "unproven node that answers becomes viable",
			conn:       unprovenConnection,
			answers:    true,
			wantProbed: true,
			wantViable: true,
		},
		{
			name:       "unproven node that never answers stays unproven",
			conn:       unprovenConnection,
			wantProbed: true,
		},
		{
			name: "seed is not probed",
			conn: func(u *url.URL) *Connection {
				conn := unprovenConnection(u)
				conn.seed = true
				return conn
			},
			answers:    true,
			wantViable: true,
		},
		{
			name: "node with a check running is not probed again",
			conn: func(u *url.URL) *Connection {
				conn := unprovenConnection(u)
				conn.mu.checkStartedAt = time.Now().UTC()
				conn.setLifecycleBit(lcHealthChecking)
				return conn
			},
			answers:      true,
			wantChecking: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var requests atomic.Int64
			tr := &Transport{
				healthCheckTimeout:          100 * time.Millisecond,
				discoveryHealthCheckRetries: 2,
				transport: mockhttp.NewRoundTripFunc(t, func(*http.Request) (*http.Response, error) {
					requests.Add(1)
					if !tt.answers {
						return nil, &mockNetError{error: http.ErrServerClosed}
					}
					return &http.Response{StatusCode: http.StatusOK, Body: validHealthCheckBody()}, nil
				}),
			}

			u, err := url.Parse("http://127.0.0.1:9200")
			require.NoError(t, err)
			conn := tt.conn(u)

			tr.probeUnprovenConnection(t.Context(), conn)

			// The probe's goroutine is done once it has cleared the running check.
			if tt.wantProbed {
				require.Eventually(t, func() bool {
					conn.mu.RLock()
					defer conn.mu.RUnlock()
					return conn.mu.checkStartedAt.IsZero()
				}, 5*time.Second, 10*time.Millisecond)
			}

			require.Equal(t, tt.wantProbed, requests.Load() > 0)
			require.Equal(t, tt.wantViable, conn.availableForRouting())
			require.Equal(t, tt.wantChecking, conn.loadConnState().lifecycle().has(lcHealthChecking))
		})
	}

	t.Run("nil connection is ignored", func(t *testing.T) {
		t.Parallel()

		(&Transport{}).probeUnprovenConnection(t.Context(), nil)
	})
}

// unprovenConnection returns a discovered connection in the state a
// singleServerPool holds it in: active, but never proven reachable.
func unprovenConnection(u *url.URL) *Connection {
	conn := &Connection{URL: u, URLString: u.String()}
	conn.setLifecycleBit(lcActive)
	return conn
}

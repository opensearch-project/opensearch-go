// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

func TestDiscoveryRequestTimeout(t *testing.T) {
	t.Parallel()

	endpoints := []struct {
		name    string
		path    string
		request func(context.Context, *Transport) error
	}{
		{
			name: "node inventory",
			path: "/_nodes/http",
			request: func(ctx context.Context, tr *Transport) error {
				return tr.DiscoverNodes(ctx)
			},
		},
		{
			name: "shard placement",
			path: "/_cat/shards",
			request: func(ctx context.Context, tr *Transport) error {
				_, err := tr.getShardPlacement(ctx)
				return err
			},
		},
		{
			name: "routing metadata",
			path: "/_cluster/state/metadata/test-index",
			request: func(ctx context.Context, tr *Transport) error {
				_, err := tr.getRoutingMeta(ctx, []string{"test-index"})
				return err
			},
		},
	}
	tests := []struct {
		name           string
		requestTimeout time.Duration
		callerTimeout  time.Duration
		flushHeaders   bool
		closeClient    bool
		wantErr        error
		wantCallerErr  error
	}{
		{
			name:           "response headers",
			requestTimeout: 50 * time.Millisecond,
			callerTimeout:  2 * time.Second,
			wantErr:        context.DeadlineExceeded,
		},
		{
			name:           "response body",
			requestTimeout: 50 * time.Millisecond,
			callerTimeout:  2 * time.Second,
			flushHeaders:   true,
			wantErr:        context.DeadlineExceeded,
		},
		{
			name:           "earlier caller deadline",
			requestTimeout: time.Second,
			callerTimeout:  50 * time.Millisecond,
			flushHeaders:   true,
			wantErr:        context.DeadlineExceeded,
			wantCallerErr:  context.DeadlineExceeded,
		},
		{
			name:          "disabled timeout",
			callerTimeout: 50 * time.Millisecond,
			flushHeaders:  true,
			wantErr:       context.DeadlineExceeded,
			wantCallerErr: context.DeadlineExceeded,
		},
		{
			name:           "client close",
			requestTimeout: time.Second,
			callerTimeout:  2 * time.Second,
			flushHeaders:   true,
			closeClient:    true,
			wantErr:        context.Canceled,
			wantCallerErr:  context.Canceled,
		},
	}

	for _, endpoint := range endpoints {
		for _, tt := range tests {
			t.Run(endpoint.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				entered := make(chan struct{})

				var stall atomic.Bool

				stall.Store(true)

				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if req.URL.Path == endpoint.path && stall.Load() {
						if tt.flushHeaders {
							w.Header().Set("Content-Type", "application/json")
							w.WriteHeader(http.StatusOK)
							w.(http.Flusher).Flush()
						}

						close(entered)
						<-req.Context().Done()

						return
					}

					w.Header().Set("Content-Type", "application/json")

					switch req.URL.Path {
					case "/_nodes/http":
						_ = json.NewEncoder(w).Encode(map[string]any{
							"nodes": map[string]nodeInfo{
								"node": {
									Name: "node", Roles: []string{RoleData},
									HTTP: nodeInfoHTTP{PublishAddress: req.Host},
								},
							},
						})
					case "/_cat/shards":
						io.WriteString(w, `[]`)
					case "/_cluster/state/metadata/test-index":
						io.WriteString(w, `{"metadata":{"indices":{}}}`)
					default:
						http.NotFound(w, req)
					}
				}))
				t.Cleanup(server.Close)

				u, err := url.Parse(server.URL)
				require.NoError(t, err)

				base := &http.Transport{}
				t.Cleanup(base.CloseIdleConnections)

				requests := make(chan context.Context, 2)
				transport := mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					if req.URL.Path == endpoint.path {
						requests <- req.Context()
					}

					return base.RoundTrip(req)
				})

				tr, err := New(Config{
					URLs:                  []*url.URL{u},
					Transport:             transport,
					RequestTimeout:        tt.requestTimeout,
					HealthCheckTimeout:    -1,
					DiscoverNodesInterval: -1,
					NodeStatsInterval:     -1,
					Router:                NewRouter(),
					HealthCheck:           NoOpHealthCheck,
					Context:               t.Context(),
					lookupEnv:             envLookup(nil),
				})
				require.NoError(t, err)
				t.Cleanup(func() {
					require.NoError(t, tr.Close())
				})

				parent := t.Context()
				if tt.closeClient {
					parent = tr.ctx
				}

				ctx, cancel := context.WithTimeout(parent, tt.callerTimeout)
				defer cancel()

				if tt.closeClient {
					go func() {
						select {
						case <-entered:
							tr.Close()
						case <-ctx.Done():
						}
					}()
				}

				err = endpoint.request(ctx, tr)

				require.ErrorIs(t, err, tt.wantErr)
				if tt.wantCallerErr != nil {
					require.ErrorIs(t, ctx.Err(), tt.wantCallerErr)
				} else {
					require.NoError(t, ctx.Err(), "the caller context must outlive the request timeout")
				}

				select {
				case <-entered:
				default:
					t.Fatal("request did not reach the stalled endpoint")
				}

				requestCtx := <-requests
				requestDeadline, ok := requestCtx.Deadline()
				require.True(t, ok)
				callerDeadline, _ := ctx.Deadline()
				if tt.requestTimeout > 0 && tt.requestTimeout < tt.callerTimeout {
					assert.True(t, requestDeadline.Before(callerDeadline), "RequestTimeout must shorten the request deadline")
				} else {
					assert.Equal(t, callerDeadline, requestDeadline, "the existing caller deadline must be preserved")
				}

				if tt.closeClient {
					return
				}

				stall.Store(false)

				recoveryCtx, recoveryCancel := context.WithTimeout(t.Context(), 2*time.Second)
				defer recoveryCancel()

				require.NoError(t, endpoint.request(recoveryCtx, tr), "a later discovery request must recover")
				assert.NoError(t, recoveryCtx.Err())

				requestCtx = <-requests
				if tt.requestTimeout > 0 {
					assert.ErrorIs(t, requestCtx.Err(), context.Canceled, "successful requests must release their timeout context")
				} else {
					assert.NoError(t, requestCtx.Err(), "a disabled timeout must leave the caller context untouched")
				}
			})
		}
	}
}

func TestShardPlacementPreservesCatalogDeadline(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), catRefreshTimeout)
	defer cancel()

	deadline, _ := ctx.Deadline()
	transport := mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
		requestDeadline, ok := req.Context().Deadline()
		assert.True(t, ok)
		assert.Equal(t, deadline, requestDeadline, "RequestTimeout must not extend the standalone catalog-refresh limit")

		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`[]`))}, nil
	})
	tr, err := New(Config{
		URLs:                  []*url.URL{testSeedURL(t)},
		Transport:             transport,
		RequestTimeout:        time.Minute,
		DiscoverNodesInterval: -1,
		NodeStatsInterval:     -1,
		Router:                NewRouter(),
		HealthCheck:           NoOpHealthCheck,
		Context:               t.Context(),
		lookupEnv:             envLookup(nil),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, tr.Close())
	})

	_, err = tr.getShardPlacement(ctx)
	require.NoError(t, err)
	assert.NoError(t, ctx.Err())
}

func TestDiscoveryRequestTimeoutPreservesInventoryAfterPartialResolution(t *testing.T) {
	t.Parallel()

	conns := []*Connection{
		routableConn(t, "fast", time.Millisecond, RoleData),
		routableConn(t, "slow", time.Millisecond, RoleData),
	}
	nodesJSON := testNodesResponse(t, map[string]nodeInfo{
		"fast": {Name: "fast", Roles: []string{RoleData}, HTTP: nodeInfoHTTP{PublishAddress: conns[0].URL.Host}},
		"slow": {Name: "slow", Roles: []string{RoleData}, HTTP: nodeInfoHTTP{PublishAddress: conns[1].URL.Host}},
	})

	var resolved, blocked atomic.Bool

	tr, err := New(Config{
		URLs:                  []*url.URL{testSeedURL(t)},
		Transport:             newResolverTestTransport(t, nodesJSON),
		RequestTimeout:        50 * time.Millisecond,
		HealthCheckTimeout:    -1,
		DiscoverNodesInterval: -1,
		NodeStatsInterval:     -1,
		Router:                NewRouter(),
		HealthCheck:           NoOpHealthCheck,
		Context:               t.Context(),
		lookupEnv:             envLookup(nil),
		MaxAddressResolvers:   2,
		AddressResolver: func(ctx context.Context, node NodeInfo) (*url.URL, error) {
			if node.ID == "fast" {
				resolved.Store(true)
				return node.URL, nil
			}

			blocked.Store(true)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, tr.Close())
	})

	initialPool := testPool(conns)
	tr.mu.Lock()
	tr.mu.connectionPool = initialPool
	tr.mu.Unlock()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	err = tr.DiscoverNodes(ctx)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, ctx.Err(), "only the discovery request deadline must expire")
	require.True(t, resolved.Load(), "one node must resolve before the request deadline")
	require.True(t, blocked.Load(), "the other resolver must observe the expired request context")

	tr.mu.RLock()
	currentPool := tr.mu.connectionPool
	tr.mu.RUnlock()
	require.Same(t, initialPool, currentPool, "a partial resolution must not replace the complete inventory")

	ready, dead := initialPool.connectionsByState()
	require.Empty(t, dead)
	require.Len(t, ready, len(conns))
	for i, conn := range conns {
		require.Same(t, conn, ready[i])
		require.True(t, conn.isReady())
	}
}

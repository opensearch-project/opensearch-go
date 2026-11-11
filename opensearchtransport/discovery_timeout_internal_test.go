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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

// discoveryTimeoutEndpoints are the internal discovery requests RequestTimeout bounds.
var discoveryTimeoutEndpoints = []struct {
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

func TestDiscoveryRequestTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		requestTimeout time.Duration
		callerTimeout  time.Duration
		flushHeaders   bool
		wantCallerErr  bool
	}{
		{
			name:           "response headers",
			requestTimeout: 500 * time.Millisecond,
			callerTimeout:  2 * time.Second,
		},
		{
			name:           "response body",
			requestTimeout: 500 * time.Millisecond,
			callerTimeout:  2 * time.Second,
			flushHeaders:   true,
		},
		{
			name:           "earlier caller deadline",
			requestTimeout: 5 * time.Second,
			callerTimeout:  500 * time.Millisecond,
			flushHeaders:   true,
			wantCallerErr:  true,
		},
		{
			name:          "disabled timeout",
			callerTimeout: 500 * time.Millisecond,
			flushHeaders:  true,
			wantCallerErr: true,
		},
	}

	for _, endpoint := range discoveryTimeoutEndpoints {
		for _, tt := range tests {
			t.Run(endpoint.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				sd := newStalledDiscovery(t, endpoint.path, tt.flushHeaders, tt.requestTimeout)

				ctx, cancel := context.WithTimeout(t.Context(), tt.callerTimeout)
				defer cancel()

				require.ErrorIs(t, endpoint.request(ctx, sd.tr), context.DeadlineExceeded)
				if tt.wantCallerErr {
					require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
				} else {
					require.NoError(t, ctx.Err(), "the caller context must outlive the request timeout")
				}
				sd.requireEntered(t)

				requestDeadline, ok := (<-sd.requests).Deadline()
				require.True(t, ok)
				callerDeadline, _ := ctx.Deadline()
				if tt.requestTimeout > 0 && tt.requestTimeout < tt.callerTimeout {
					require.True(t, requestDeadline.Before(callerDeadline), "RequestTimeout must shorten the request deadline")
				} else {
					require.Equal(t, callerDeadline, requestDeadline, "the existing caller deadline must be preserved")
				}

				sd.stall.Store(false)

				recoveryCtx, recoveryCancel := context.WithTimeout(t.Context(), 2*time.Second)
				defer recoveryCancel()

				require.NoError(t, endpoint.request(recoveryCtx, sd.tr), "a later discovery request must recover")
				require.NoError(t, recoveryCtx.Err())

				requestCtx := <-sd.requests
				if tt.requestTimeout > 0 {
					require.ErrorIs(t, requestCtx.Err(), context.Canceled, "successful requests must release their timeout context")
				} else {
					require.NoError(t, requestCtx.Err(), "a disabled timeout must leave the caller context untouched")
				}
			})
		}
	}
}

func TestDiscoveryRequestTimeoutClientClose(t *testing.T) {
	t.Parallel()

	for _, endpoint := range discoveryTimeoutEndpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			t.Parallel()

			sd := newStalledDiscovery(t, endpoint.path, true, time.Second)

			ctx, cancel := context.WithTimeout(sd.tr.ctx, 2*time.Second)
			defer cancel()

			go func() {
				select {
				case <-sd.entered:
					sd.tr.Close()
				case <-ctx.Done():
				}
			}()

			require.ErrorIs(t, endpoint.request(ctx, sd.tr), context.Canceled)
			require.ErrorIs(t, ctx.Err(), context.Canceled)
			sd.requireEntered(t)
		})
	}
}

// stalledDiscovery is a transport whose server holds the stalled path open
// until the request context ends, while stall is set.
type stalledDiscovery struct {
	tr       *Transport
	entered  chan struct{}        // closed when the stalled path is first reached
	stall    *atomic.Bool         // clear to let the stalled path answer
	requests chan context.Context // context of each request to the stalled path
}

func newStalledDiscovery(t *testing.T, stalledPath string, flushHeaders bool, requestTimeout time.Duration) *stalledDiscovery {
	t.Helper()

	sd := &stalledDiscovery{
		entered:  make(chan struct{}),
		stall:    new(atomic.Bool),
		requests: make(chan context.Context, 2),
	}
	sd.stall.Store(true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == stalledPath && sd.stall.Load() {
			if flushHeaders {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
			}

			close(sd.entered)
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

	sd.tr = newDiscoveryTimeoutTransport(t, Config{
		URLs: []*url.URL{u},
		Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == stalledPath {
				sd.requests <- req.Context()
			}

			return base.RoundTrip(req)
		}),
		RequestTimeout: requestTimeout,
	})

	return sd
}

func (sd *stalledDiscovery) requireEntered(t *testing.T) {
	t.Helper()

	select {
	case <-sd.entered:
	default:
		require.FailNow(t, "request did not reach the stalled endpoint")
	}
}

// newDiscoveryTimeoutTransport builds a transport from cfg with background
// discovery, node stats and health checks turned off, and closes it at cleanup.
func newDiscoveryTimeoutTransport(t *testing.T, cfg Config) *Transport {
	t.Helper()

	cfg.HealthCheckTimeout = -1
	cfg.DiscoverNodesInterval = -1
	cfg.NodeStatsInterval = -1
	cfg.Router = NewRouter()
	cfg.HealthCheck = NoOpHealthCheck
	cfg.Context = t.Context()
	cfg.lookupEnv = envLookup(nil)

	tr, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, tr.Close())
	})

	return tr
}

func TestShardPlacementPreservesCatalogDeadline(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), catRefreshTimeout)
	defer cancel()

	deadline, _ := ctx.Deadline()
	tr := newDiscoveryTimeoutTransport(t, Config{
		URLs: []*url.URL{testSeedURL(t)},
		Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
			requestDeadline, ok := req.Context().Deadline()
			require.True(t, ok)
			require.Equal(t, deadline, requestDeadline, "RequestTimeout must not extend the standalone catalog-refresh limit")

			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`[]`))}, nil
		}),
		RequestTimeout: time.Minute,
	})

	_, err := tr.getShardPlacement(ctx)
	require.NoError(t, err)
	require.NoError(t, ctx.Err())
}

func TestDiscoveryRequestTimeoutLeavesAddressResolutionToCaller(t *testing.T) {
	t.Parallel()

	conns := []*Connection{
		routableConn(t, "first", time.Millisecond, RoleData),
		routableConn(t, "second", time.Millisecond, RoleData),
	}
	nodesJSON := testNodesResponse(t, map[string]nodeInfo{
		"first":  {Name: "first", Roles: []string{RoleData}, HTTP: nodeInfoHTTP{PublishAddress: conns[0].URL.Host}},
		"second": {Name: "second", Roles: []string{RoleData}, HTTP: nodeInfoHTTP{PublishAddress: conns[1].URL.Host}},
	})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	callerDeadline, _ := ctx.Deadline()

	var resolved atomic.Int32

	tr := newDiscoveryTimeoutTransport(t, Config{
		URLs:                []*url.URL{testSeedURL(t)},
		Transport:           newResolverTestTransport(t, nodesJSON),
		RequestTimeout:      time.Second,
		MaxAddressResolvers: 2,
		AddressResolver: func(resolverCtx context.Context, node NodeInfo) (*url.URL, error) {
			deadline, ok := resolverCtx.Deadline()
			if !ok || !deadline.Equal(callerDeadline) || resolverCtx.Err() != nil {
				return nil, fmt.Errorf("resolver for %q got deadline %v (set %t); want the caller's %v: %w",
					node.ID, deadline, ok, callerDeadline, resolverCtx.Err())
			}
			resolved.Add(1)
			return node.URL, nil
		},
	})

	tr.mu.Lock()
	tr.mu.connectionPool = testPool(conns)
	tr.mu.Unlock()

	require.NoError(t, tr.DiscoverNodes(ctx), "RequestTimeout must bound only the HTTP request, not address resolution")
	require.EqualValues(t, len(conns), resolved.Load())
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

func TestStartupSeedFailureDoesNotPanic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		poolFunc func([]*Connection, Selector) ConnectionPool
		afterNew func(t *testing.T, tr *Transport)
	}{
		{name: "default pool"},
		{
			name: "connection pool factory that keeps its input slice",
			poolFunc: func(conns []*Connection, _ Selector) ConnectionPool {
				pool := NewConnectionPool(conns, nil).(*multiServerPool)
				pool.mu.ready = conns

				return pool
			},
			afterNew: func(t *testing.T, tr *Transport) {
				t.Helper()

				_, err := tr.mu.connectionPool.Next()
				require.NoError(t, err)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				router, err := NewDefaultRouter()
				require.NoError(t, err)

				networkErr := &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}
				var failedSeed atomic.Pointer[url.URL]

				transport := mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					if req.URL.Path == "/_nodes/http" {
						failedSeed.Store(req.URL)
						return nil, networkErr
					}

					if failed := failedSeed.Load(); failed != nil && req.URL.Host == failed.Host {
						return nil, networkErr
					}

					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(`{}`)),
						Header:     make(http.Header),
						Request:    req,
					}, nil
				})
				cfg := Config{
					URLs: []*url.URL{
						{Scheme: "http", Host: "127.0.0.1:19200"},
						{Scheme: "http", Host: "127.0.0.1:19201"},
						{Scheme: "http", Host: "127.0.0.1:19202"},
					},
					Context:               t.Context(),
					Transport:             transport,
					Router:                router,
					SkipConnectionShuffle: true,
					DiscoverNodesInterval: -1,
					NodeStatsInterval:     -1,
					HealthCheck: func(_ context.Context, _ *Connection, u *url.URL) (*http.Response, error) {
						if failed := failedSeed.Load(); failed != nil && u.Host == failed.Host {
							return nil, networkErr
						}

						return &http.Response{
							StatusCode: http.StatusOK,
							Body:       io.NopCloser(strings.NewReader(validOpenSearchRootResponse())),
							Header:     make(http.Header),
						}, nil
					},
					ConnectionPoolFunc: tt.poolFunc,
				}

				tr, err := New(cfg)
				require.NoError(t, err)
				defer func() {
					require.NoError(t, tr.Close())
				}()

				if tt.afterNew != nil {
					tt.afterNew(t, tr)
				}

				// Fail discovery before the asynchronous seed loop has read every seed.
				_, err = tr.getNodesInfo(t.Context())
				require.ErrorIs(t, err, networkErr)

				synctest.Wait()

				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/index/_search", nil)
				require.NoError(t, err)

				res, err := tr.Request(req)
				require.NoError(t, err)
				require.NoError(t, res.Body.Close())
				require.Equal(t, http.StatusOK, res.StatusCode)
				require.NotNil(t, res.Request)
				require.NotEqual(t, failedSeed.Load().Host, res.Request.URL.Host)
			})
		})
	}
}

func TestNewConnectionPoolCopiesItsInput(t *testing.T) {
	t.Parallel()

	conns := []*Connection{
		{URL: &url.URL{Scheme: "http", Host: "127.0.0.1:19200"}},
		{URL: &url.URL{Scheme: "http", Host: "127.0.0.1:19201"}},
		{URL: &url.URL{Scheme: "http", Host: "127.0.0.1:19202"}},
	}
	want := slices.Clone(conns)

	pool, ok := NewConnectionPool(conns, nil).(*multiServerPool)
	require.True(t, ok)

	conns[0], conns[2] = nil, nil

	pool.mu.RLock()
	defer pool.mu.RUnlock()
	require.Equal(t, want, pool.mu.ready, "the pool must not share the caller's slice")
}

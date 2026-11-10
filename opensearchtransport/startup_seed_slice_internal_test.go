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
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

func TestStartupSeedFailureDoesNotPanic(t *testing.T) {
	t.Parallel()

	const childEnv = "OPENSEARCH_TEST_STARTUP_SEED_CHILD"

	poolMode := os.Getenv(childEnv)
	if poolMode == "" {
		for _, mode := range []string{"default", "factory"} {
			t.Run(mode, func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()

				//nolint:gosec // Reexecutes this test binary with fixed arguments and no shell.
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStartupSeedFailureDoesNotPanic$", "-test.v")
				cmd.Env = append(os.Environ(), childEnv+"="+mode, "GOMAXPROCS=1")
				output, err := cmd.CombinedOutput()
				require.NoError(t, err, "startup with a failed seed must not panic:\n%s", output)
			})
		}

		return
	}

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
		}
		if poolMode == "factory" {
			cfg.ConnectionPoolFunc = func(conns []*Connection, _ Selector) ConnectionPool {
				return NewConnectionPool(conns, nil)
			}
		}

		tr, err := New(cfg)
		require.NoError(t, err)
		defer func() {
			require.NoError(t, tr.Close())
		}()

		if poolMode == "factory" {
			_, err = tr.mu.connectionPool.Next()
			require.NoError(t, err)
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
}

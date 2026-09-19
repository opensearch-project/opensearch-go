// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRetryDoesNotMutateInFlightRequest guards the retry loop against mutating a
// request the transport still holds. A timed-out HTTP/2 attempt returns to
// stream() while the transport's stream goroutine for that attempt is still
// holding the request it was given, and that request shares its *url.URL and
// Header map with the one stream() rewrites for the next attempt. net/http
// forbids mutating a request once RoundTrip has it, and the rewrite is only
// reported as a data race under -race.
//
// The assertions hold without the detector too: every attempt must arrive at the
// connection base path joined to the caller path, carrying the caller's body,
// which is what a torn read, a stacked prefix, or a retry copy that lost its
// body would break on the wire.
func TestRetryDoesNotMutateInFlightRequest(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		method string
		// body, when set, is also what enables request-body logging, so the row
		// covers the rewind stream() performs after each attempt as well as the
		// body being carried onto each retry's request.
		body string
	}{
		{
			// The URL rewrite the retry loop performs on entry to each attempt.
			name:   "url rewritten for the next attempt",
			method: http.MethodGet,
		},
		{
			name:   "body carried onto every attempt",
			method: http.MethodPost,
			body:   `{"query":{"match_all":{}}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			const (
				wantPath = "/prefix/logs-000001/_search"
				stalls   = 2
				// attemptTimeout bounds each attempt. Kept well above CI
				// scheduling granularity: the first attempt has to finish a TCP
				// connect, a TLS handshake and the HTTP/2 preface and still reach
				// the handler inside it, and the last attempt a full round trip.
				attemptTimeout = time.Second
			)

			var (
				attempts atomic.Int64
				observed struct {
					sync.Mutex
					paths  []string
					bodies []string
				}
			)

			backend := newTLSHTTP2Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)

				observed.Lock()
				observed.paths = append(observed.paths, r.URL.Path)
				observed.bodies = append(observed.bodies, string(body))
				observed.Unlock()

				// Stall every attempt but the last so each one hits the
				// per-attempt timeout and the retry loop rewrites the request
				// behind it.
				if attempts.Add(1) <= stalls {
					<-r.Context().Done()
					return
				}
				_, _ = io.WriteString(w, "{}")
			}))

			dialer := &net.Dialer{Timeout: time.Second}
			httpTransport := http.DefaultTransport.(*http.Transport).Clone()
			httpTransport.ForceAttemptHTTP2 = true
			httpTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // local httptest only
			httpTransport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "tcp", backend.Listener.Addr().String())
			}

			// A base path on the connection URL makes setReqURL prepend on every
			// attempt, which is both the write that races and the one that stacks.
			u, err := url.Parse("https://cluster.test/prefix")
			require.NoError(t, err)

			var logger Logger
			if tc.body != "" {
				logger = &logCapture{reqBodyEnabled: true}
			}

			transport, err := New(Config{
				URLs:                 []*url.URL{u},
				Transport:            httpTransport,
				Logger:               logger,
				MaxRetries:           stalls,
				EnableRetryOnTimeout: true,
				RequestTimeout:       attemptTimeout,
				HealthCheck:          NoOpHealthCheck,
				NodeStatsInterval:    -1,
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = transport.Close() })

			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req, err := http.NewRequestWithContext(t.Context(), tc.method, "/logs-000001/_search", body)
			require.NoError(t, err)

			res, err := transport.Stream(req)
			require.NoError(t, err, "the final attempt must reach the backend")
			_, _ = io.Copy(io.Discard, res.Body)
			require.NoError(t, res.Body.Close())
			require.Equal(t, 2, res.ProtoMajor, "test requires HTTP/2")

			observed.Lock()
			defer observed.Unlock()
			require.Len(t, observed.paths, stalls+1, "every attempt should reach the backend")
			for i, got := range observed.paths {
				require.Equal(t, wantPath, got, "attempt %d path", i)
			}
			for i, got := range observed.bodies {
				require.Equal(t, tc.body, got, "attempt %d body", i)
			}
		})
	}
}

// newTLSHTTP2Server starts an httptest server serving HTTP/2 over TLS, which is
// the only protocol that reproduces a retry racing an abandoned attempt.
func newTLSHTTP2Server(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

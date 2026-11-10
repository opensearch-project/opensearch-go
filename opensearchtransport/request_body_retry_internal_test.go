// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

// failingBody yields data, then err.
type failingBody struct {
	data string
	err  error
	read bool
}

func (b *failingBody) Read(p []byte) (int, error) {
	if b.read {
		return 0, b.err
	}
	b.read = true
	return copy(p, b.data), nil
}

func (*failingBody) Close() error { return nil }

// roundTrips records what each round trip saw.
type roundTrips struct {
	mu struct {
		sync.Mutex
		hosts  []string
		bodies []string
	}
}

func (rt *roundTrips) record(req *http.Request) {
	var sent []byte
	if req.Body != nil {
		sent, _ = io.ReadAll(req.Body)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.mu.hosts = append(rt.mu.hosts, req.URL.Host)
	rt.mu.bodies = append(rt.mu.bodies, string(sent))
}

func (rt *roundTrips) snapshot() ([]string, []string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return append([]string(nil), rt.mu.hosts...), append([]string(nil), rt.mu.bodies...)
}

const (
	query   = `{"query":{"match_all":{}}}`
	partial = `{"hits":`
	okJSON  = `{"hits":[]}`
)

// okResponse is a 200 response carrying body.
func okResponse(body io.ReadCloser) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{}, Body: body}
}

// twoNodes returns the URLs of two nodes, so a failed attempt has somewhere to retry.
func twoNodes() []*url.URL {
	return []*url.URL{{Scheme: "http", Host: "node-a:9200"}, {Scheme: "http", Host: "node-b:9200"}}
}

// requireErrs requires err to be nil when want is empty, and otherwise to match
// every error in want.
func requireErrs(t *testing.T, err error, want []error) {
	t.Helper()

	if len(want) == 0 {
		require.NoError(t, err)
	}
	for _, w := range want {
		require.ErrorIs(t, err, w)
	}
}

func deadConns(tp *Transport) int {
	tp.mu.RLock()
	pool := tp.mu.connectionPool.(*multiServerPool)
	tp.mu.RUnlock()
	pool.mu.RLock()
	defer pool.mu.RUnlock()
	return len(pool.mu.dead)
}

func TestRequestBodyReadFailure(t *testing.T) {
	t.Parallel()

	truncated := func() io.ReadCloser { return &failingBody{data: partial, err: io.ErrUnexpectedEOF} }
	timedOut := func() io.ReadCloser { return &failingBody{data: partial, err: context.DeadlineExceeded} }
	healthy := func() io.ReadCloser { return io.NopCloser(strings.NewReader(okJSON)) }

	tests := []struct {
		name              string
		cfg               Config
		method            string
		path              string
		bodies            []func() io.ReadCloser // per trip; the last repeats
		wantTrips         int
		wantErr           []error
		wantBody          string
		wantDead          int
		wantDistinctHosts bool
	}{
		{
			name:              "truncated 200 on a read retries on another node",
			cfg:               Config{MaxRetries: 1},
			method:            http.MethodPost,
			path:              "/documents/_search",
			bodies:            []func() io.ReadCloser{truncated, healthy},
			wantTrips:         2,
			wantBody:          okJSON,
			wantDead:          1,
			wantDistinctHosts: true,
		},
		{
			name:              "truncated 200 on a document get retries on another node",
			cfg:               Config{MaxRetries: 1},
			method:            http.MethodGet,
			path:              "/idx/_doc/1",
			bodies:            []func() io.ReadCloser{truncated, healthy},
			wantTrips:         2,
			wantBody:          okJSON,
			wantDead:          1,
			wantDistinctHosts: true,
		},
		{
			name:      "DisableRetry returns the partial response",
			cfg:       Config{MaxRetries: 1, DisableRetry: true},
			method:    http.MethodPost,
			path:      "/documents/_search",
			bodies:    []func() io.ReadCloser{truncated, healthy},
			wantTrips: 1,
			wantErr:   []error{ErrResponseBodyRead, io.ErrUnexpectedEOF},
			wantBody:  partial,
			wantDead:  1,
		},
		{
			name:      "every attempt failing returns the last partial response",
			cfg:       Config{MaxRetries: 1},
			method:    http.MethodPost,
			path:      "/documents/_search",
			bodies:    []func() io.ReadCloser{truncated},
			wantTrips: 2,
			wantErr:   []error{ErrResponseBodyRead, io.ErrUnexpectedEOF},
			wantBody:  partial,
			wantDead:  2,
		},
		{
			name:      "a write is not replayed but the node is marked failed",
			cfg:       Config{MaxRetries: 1},
			method:    http.MethodPost,
			path:      "/idx/_doc",
			bodies:    []func() io.ReadCloser{truncated, healthy},
			wantTrips: 1,
			wantErr:   []error{ErrResponseBodyRead, io.ErrUnexpectedEOF},
			wantBody:  partial,
			wantDead:  1,
		},
		{
			name:      "a scroll POST is not replayed because the cursor has advanced",
			cfg:       Config{MaxRetries: 1},
			method:    http.MethodPost,
			path:      "/_search/scroll",
			bodies:    []func() io.ReadCloser{truncated, healthy},
			wantTrips: 1,
			wantErr:   []error{ErrResponseBodyRead, io.ErrUnexpectedEOF},
			wantBody:  partial,
			wantDead:  1,
		},
		{
			name:      "a scroll GET is not replayed because the cursor has advanced",
			cfg:       Config{MaxRetries: 1},
			method:    http.MethodGet,
			path:      "/_search/scroll",
			bodies:    []func() io.ReadCloser{truncated, healthy},
			wantTrips: 1,
			wantErr:   []error{ErrResponseBodyRead, io.ErrUnexpectedEOF},
			wantBody:  partial,
			wantDead:  1,
		},
		{
			name:      "a timeout during the body read is not retried by default",
			cfg:       Config{MaxRetries: 1},
			method:    http.MethodPost,
			path:      "/documents/_search",
			bodies:    []func() io.ReadCloser{timedOut, healthy},
			wantTrips: 1,
			wantErr:   []error{ErrResponseBodyRead, context.DeadlineExceeded},
			wantBody:  partial,
			wantDead:  1,
		},
		{
			name:              "a timeout during the body read retries with EnableRetryOnTimeout",
			cfg:               Config{MaxRetries: 1, EnableRetryOnTimeout: true},
			method:            http.MethodPost,
			path:              "/documents/_search",
			bodies:            []func() io.ReadCloser{timedOut, healthy},
			wantTrips:         2,
			wantBody:          okJSON,
			wantDead:          1,
			wantDistinctHosts: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var rec roundTrips
			var calls atomic.Int64
			cfg := tt.cfg
			cfg.URLs = twoNodes()
			cfg.NodeStatsInterval = -1
			cfg.Transport = mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				rec.record(req)
				i := min(int(calls.Add(1))-1, len(tt.bodies)-1)
				return okResponse(tt.bodies[i]()), nil
			})
			tp, err := New(cfg)
			require.NoError(t, err)
			t.Cleanup(func() { _ = tp.Close() })

			req, err := http.NewRequestWithContext(t.Context(), tt.method, tt.path, strings.NewReader(query))
			require.NoError(t, err)

			res, err := tp.Request(req)
			require.NotNil(t, res)
			defer res.Body.Close()

			hosts, bodies := rec.snapshot()
			require.Len(t, hosts, tt.wantTrips)
			for _, b := range bodies {
				require.JSONEq(t, query, b)
			}
			if tt.wantDistinctHosts {
				require.NotEqual(t, hosts[0], hosts[1])
			}
			requireErrs(t, err, tt.wantErr)
			got, rerr := io.ReadAll(res.Body)
			require.NoError(t, rerr)
			require.Equal(t, tt.wantBody, string(got))
			require.Equal(t, tt.wantDead, deadConns(tp))
		})
	}
}

// A caller cancellation or deadline during the body read is neither retried nor
// held against the node.
func TestRequestBodyReadCallerCancel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		deadline bool
		wantErr  error
	}{
		{name: "cancel", wantErr: context.Canceled},
		{name: "deadline", deadline: true, wantErr: context.DeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				ctx    context.Context
				cancel context.CancelFunc
			)
			if tt.deadline {
				ctx, cancel = context.WithTimeout(t.Context(), time.Second)
			} else {
				ctx, cancel = context.WithCancel(t.Context())
			}
			defer cancel()

			var rec roundTrips
			reading := make(chan struct{})
			tp, err := New(Config{
				URLs:              twoNodes(),
				NodeStatsInterval: -1,
				MaxRetries:        1,
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					rec.record(req)
					return okResponse(&blockingBody{ctx: req.Context(), reading: reading}), nil
				}),
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = tp.Close() })

			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/documents/_search", strings.NewReader("{}"))
			require.NoError(t, err)

			if !tt.deadline {
				go func() {
					<-reading
					cancel()
				}()
			}

			res, err := tp.Request(req)
			if res != nil && res.Body != nil {
				defer res.Body.Close()
			}

			hosts, _ := rec.snapshot()
			require.Len(t, hosts, 1)
			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, 0, deadConns(tp))
		})
	}
}

// The byte count reported for a request is the final attempt's: a retry that
// ends with no response does not inherit an earlier attempt's partial body,
// whether its round trip returned none or it never reached a round trip.
func TestRequestResponseBytesIsFinalAttempt(t *testing.T) {
	t.Parallel()

	errGetBody := errors.New("get body")
	tests := []struct {
		name    string
		getBody func() (io.ReadCloser, error) // nil keeps the request's own
		wantErr error
	}{
		{name: "retry round trip returns no response", wantErr: io.ErrUnexpectedEOF},
		{
			name:    "retry cannot rebuild the request body",
			getBody: func() (io.ReadCloser, error) { return nil, errGetBody },
			wantErr: errGetBody,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs := newRecordingObserver()
			var calls atomic.Int64
			tp, err := New(Config{
				URLs:              twoNodes(),
				NodeStatsInterval: -1,
				MaxRetries:        1,
				Observer:          obs,
				Transport: mockhttp.NewRoundTripFunc(t, func(*http.Request) (*http.Response, error) {
					if calls.Add(1) > 1 {
						return nil, io.ErrUnexpectedEOF
					}
					return okResponse(&failingBody{data: partial, err: io.ErrUnexpectedEOF}), nil
				}),
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = tp.Close() })

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/documents/_search", strings.NewReader("{}"))
			require.NoError(t, err)
			if tt.getBody != nil {
				req.GetBody = tt.getBody
			}

			res, err := tp.Request(req)
			if res != nil && res.Body != nil {
				defer res.Body.Close()
			}
			require.Nil(t, res)
			require.ErrorIs(t, err, tt.wantErr)

			events := obs.getReqRespEvents()
			require.Len(t, events, 1)
			require.Equal(t, int64(0), events[0].ResponseBytes)
		})
	}
}

// RequestTimeout bounds the body read of a real HTTP response: a server that
// sends headers and part of the body and then stalls fails the attempt, and the
// request is retried on the other node only when EnableRetryOnTimeout is set.
func TestRequestTimeoutCoversBodyRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		retryTimeouts bool
		wantRequests  int
		wantErr       []error
		wantBody      string
	}{
		{
			name:          "retried with EnableRetryOnTimeout",
			retryTimeouts: true,
			wantRequests:  2,
			wantBody:      okJSON,
		},
		{
			name:         "not retried by default",
			wantRequests: 1,
			wantErr:      []error{ErrResponseBodyRead, context.DeadlineExceeded},
			wantBody:     partial,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				requests atomic.Int64
				stalled  atomic.Bool
				release  = make(chan struct{})
			)
			// Whichever node is asked first stalls; the other answers. Which
			// node that is depends on pool order, so roles are not fixed.
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if stalled.CompareAndSwap(false, true) {
					w.Header().Set("Content-Length", strconv.Itoa(len(okJSON)))
					w.WriteHeader(http.StatusOK)
					_, _ = io.WriteString(w, partial)
					w.(http.Flusher).Flush()
					select {
					case <-r.Context().Done():
					case <-release:
					}
					return
				}
				_, _ = io.WriteString(w, okJSON)
			})
			a, b := httptest.NewServer(handler), httptest.NewServer(handler)
			t.Cleanup(a.Close)
			t.Cleanup(b.Close)
			t.Cleanup(func() { close(release) })

			tp, err := New(Config{
				URLs:                 []*url.URL{mustParseURL(a.URL), mustParseURL(b.URL)},
				NodeStatsInterval:    -1,
				MaxRetries:           1,
				RequestTimeout:       time.Second,
				EnableRetryOnTimeout: tt.retryTimeouts,
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = tp.Close() })

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/documents/_search", strings.NewReader("{}"))
			require.NoError(t, err)

			res, err := tp.Request(req)
			require.NotNil(t, res)
			defer res.Body.Close()

			require.Equal(t, int64(tt.wantRequests), requests.Load())
			requireErrs(t, err, tt.wantErr)
			got, rerr := io.ReadAll(res.Body)
			require.NoError(t, rerr)
			require.Equal(t, tt.wantBody, string(got))
		})
	}
}

// blockingBody blocks its first Read until ctx is done, then returns ctx.Err().
type blockingBody struct {
	ctx     context.Context //nolint:containedctx // test stub
	reading chan struct{}
	once    sync.Once
}

func (b *blockingBody) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.reading) })
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (*blockingBody) Close() error { return nil }

// A logger that records response bodies replaces the response body after the
// attempt; Request must still return the body and report its byte count.
func TestRequestWithResponseBodyLogging(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      func() io.ReadCloser
		wantErr   error
		wantBody  string
		wantBytes int64
	}{
		{
			name:      "success",
			body:      func() io.ReadCloser { return io.NopCloser(strings.NewReader(okJSON)) },
			wantBody:  okJSON,
			wantBytes: int64(len(okJSON)),
		},
		{
			name:      "body read failure",
			body:      func() io.ReadCloser { return &failingBody{data: partial, err: io.ErrUnexpectedEOF} },
			wantErr:   ErrResponseBodyRead,
			wantBody:  partial,
			wantBytes: int64(len(partial)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs := newRecordingObserver()
			var out strings.Builder
			tp, err := New(Config{
				URLs:              []*url.URL{{Scheme: "http", Host: "node-a:9200"}},
				NodeStatsInterval: -1,
				DisableRetry:      true,
				Observer:          obs,
				Logger:            &TextLogger{Output: &out, EnableResponseBody: true},
				Transport: mockhttp.NewRoundTripFunc(t, func(*http.Request) (*http.Response, error) {
					return okResponse(tt.body()), nil
				}),
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = tp.Close() })

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/_search", nil)
			require.NoError(t, err)

			res, err := tp.Request(req)
			require.NotNil(t, res)
			defer res.Body.Close()
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			got, rerr := io.ReadAll(res.Body)
			require.NoError(t, rerr)
			require.Equal(t, tt.wantBody, string(got))

			events := obs.getReqRespEvents()
			require.Len(t, events, 1)
			require.Equal(t, tt.wantBytes, events[0].ResponseBytes)
		})
	}
}

// Request tolerates a RoundTripper that returns no body, or a response together
// with an error.
func TestRequestUnusualRoundTripResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		body           io.ReadCloser
		err            error
		requestTimeout time.Duration
		wantErr        error
	}{
		{
			name: "nil body",
		},
		{
			// The per-attempt context is cancelled at once rather than tied to
			// closing a body that is not there.
			name:           "nil body under a per-attempt timeout",
			requestTimeout: time.Second,
		},
		{
			name:    "response with error",
			body:    io.NopCloser(strings.NewReader("x")),
			err:     io.ErrUnexpectedEOF,
			wantErr: io.ErrUnexpectedEOF,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs := newRecordingObserver()
			tp, err := New(Config{
				URLs:              []*url.URL{{Scheme: "http", Host: "node-a:9200"}},
				NodeStatsInterval: -1,
				DisableRetry:      true,
				RequestTimeout:    tt.requestTimeout,
				Observer:          obs,
				Transport: mockhttp.NewRoundTripFunc(t, func(*http.Request) (*http.Response, error) {
					return okResponse(tt.body), tt.err
				}),
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = tp.Close() })

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/_search", nil)
			require.NoError(t, err)

			res, err := tp.Request(req)
			if res != nil && res.Body != nil {
				defer res.Body.Close()
			}
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			if tt.body == nil {
				require.NotNil(t, res)
				require.Nil(t, res.Body)
			}
			events := obs.getReqRespEvents()
			require.Len(t, events, 1)
			require.Equal(t, int64(0), events[0].ResponseBytes)
		})
	}
}

// A failed body read is judged like a failed round trip on every path a request
// can take: the shard-aware router, the connection pool, and the seed fallback.
// A read that fails, or times out under the per-attempt timeout, demotes the
// node that served it; a caller cancellation during the read leaves it healthy.
// Either way the caller gets the partial body with an error matching
// ErrResponseBodyRead.
func TestBodyReadFailureConnectionHealth(t *testing.T) {
	t.Parallel()

	var (
		poolPath         = callerHealthPath{name: "pool", newTransport: newPoolPathTransport}
		routerPath       = callerHealthPath{name: "router", newTransport: newRouterPathTransport, routed: true}
		seedFallbackPath = callerHealthPath{name: "seed fallback", newTransport: newSeedFallbackPathTransport, seedFallback: true}
	)

	tests := []struct {
		name string
		// requestTimeout sets the transport's per-attempt timeout.
		requestTimeout time.Duration
		// body is the response body; cancel cancels the caller's context.
		body func(ctx context.Context, cancel context.CancelFunc) io.ReadCloser
		// preservesHealth: the node stays ready; otherwise it is demoted.
		preservesHealth bool
		wantErr         error
		wantDrains      int64
	}{
		{
			name: "truncated body",
			body: func(context.Context, context.CancelFunc) io.ReadCloser {
				return &failingBody{data: partial, err: io.ErrUnexpectedEOF}
			},
			wantErr: io.ErrUnexpectedEOF,
		},
		{
			name:           "attempt timeout during the read",
			requestTimeout: time.Second,
			body: func(ctx context.Context, _ context.CancelFunc) io.ReadCloser {
				return &stallingBody{data: partial, ctx: ctx}
			},
			wantErr:    context.DeadlineExceeded,
			wantDrains: 1,
		},
		{
			name: "caller cancel during the read",
			body: func(ctx context.Context, cancel context.CancelFunc) io.ReadCloser {
				return &stallingBody{data: partial, ctx: ctx, onStall: cancel}
			},
			preservesHealth: true,
			wantErr:         context.Canceled,
		},
	}

	for _, tt := range tests {
		for _, path := range []callerHealthPath{poolPath, routerPath, seedFallbackPath} {
			t.Run(path.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithCancel(t.Context())
				t.Cleanup(cancel)

				obs := &callerHealthObserver{recordingObserver: newRecordingObserver()}
				tr, pool, conns := path.newTransport(t, obs)
				tr.disableRetry = true
				tr.requestTimeout = tt.requestTimeout

				var (
					selected *Connection
					attempts int
				)
				tr.transport = mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					attempts++
					for _, conn := range conns {
						if conn.URL.Host == req.URL.Host {
							selected = conn
						}
					}
					return okResponse(tt.body(req.Context(), cancel)), nil
				})

				req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/index/_doc/1", nil)
				require.NoError(t, err)

				res, err := tr.Request(req)
				require.NotNil(t, res)
				defer res.Body.Close()

				require.ErrorIs(t, err, ErrResponseBodyRead)
				require.ErrorIs(t, err, tt.wantErr)
				got, rerr := io.ReadAll(res.Body)
				require.NoError(t, rerr)
				require.Equal(t, partial, string(got))
				require.Equal(t, 1, attempts)
				require.NotNil(t, selected)
				require.Equal(t, tt.wantDrains, selected.drainingConn.Load())

				for _, conn := range conns {
					wantDead := conn == selected && !tt.preservesHealth
					require.Equal(t, wantDead, conn.loadConnState().lifecycle().has(lcDead), conn.Name)
					require.EqualValues(t, boolToInt(wantDead), conn.failures.Load(), conn.Name)
				}
				if !path.routed {
					_, dead := pool.connectionsByState()
					require.Len(t, dead, boolToInt(!tt.preservesHealth))
				}
				if !path.seedFallback {
					require.Equal(t, !tt.preservesHealth, selected.needsCatUpdate())
				}

				events := obs.getReqRespEvents()
				require.Len(t, events, 1)
				require.Equal(t, int64(len(partial)), events[0].ResponseBytes)
			})
		}
	}
}

// stallingBody yields data, then blocks until ctx ends and returns its error.
// onStall, when set, runs as the read starts to block.
type stallingBody struct {
	data    string
	ctx     context.Context //nolint:containedctx // test stub bound to one attempt
	onStall func()
	sent    bool
}

func (b *stallingBody) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		return copy(p, b.data), nil
	}
	if b.onStall != nil {
		b.onStall()
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (*stallingBody) Close() error { return nil }

func TestStreamRejectsUnsetBodyMode(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res, _, err := (&Transport{}).stream(req, bodyUnknown) //nolint:bodyclose // no response on this path
	require.ErrorIs(t, err, errBodyModeUnset)
	require.Nil(t, res)
}

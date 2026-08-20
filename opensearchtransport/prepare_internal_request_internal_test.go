// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/signer"
)

const (
	testSigV4Auth       = "AWS4-HMAC-SHA256 signed"
	headerAuthorization = "authorization" // Header.Get/Set canonicalize this to Authorization
)

// recordingSigner records every request it is asked to sign and stamps
// Authorization so the RoundTripper can assert the stamp made it onto
// the wire. err, if set, is returned after the request is recorded.
type recordingSigner struct {
	mu struct {
		sync.Mutex
		reqs []recordedSign
		err  error
	}
}

type recordedSign struct {
	method string
	path   string
	ua     string
	extra  string
}

func (s *recordingSigner) SignRequest(req *http.Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mu.reqs = append(s.mu.reqs, recordedSign{
		method: req.Method,
		path:   req.URL.Path,
		ua:     req.Header.Get("User-Agent"),
		extra:  req.Header.Get("X-Test-Header"),
	})
	if s.mu.err != nil {
		return s.mu.err
	}
	req.Header.Set(headerAuthorization, testSigV4Auth)
	return nil
}

func (s *recordingSigner) OverrideSigningPort(uint16) {}

func (s *recordingSigner) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.mu.reqs))
	for i, r := range s.mu.reqs {
		out[i] = r.path
	}
	return out
}

func (s *recordingSigner) recorded() []recordedSign {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]recordedSign, len(s.mu.reqs))
	copy(out, s.mu.reqs)
	return out
}

func failingSigner(err error) *recordingSigner {
	s := &recordingSigner{}
	s.mu.err = err
	return s
}

// captureTripper records the request that actually left the process and
// returns a canned response. Header values are snapshotted at RoundTrip
// time so a later mutation of the original request cannot hide a miss.
type captureTripper struct {
	mu struct {
		sync.Mutex
		reqs []capturedReq
	}
	body   []byte
	code   int
	bodies map[string][]byte
}

type capturedReq struct {
	method string
	path   string
	auth   string
	ua     string
	extra  string
	mod    string
}

func (t *captureTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.mu.reqs = append(t.mu.reqs, capturedReq{
		method: req.Method,
		path:   req.URL.Path,
		auth:   req.Header.Get(headerAuthorization),
		ua:     req.Header.Get("User-Agent"),
		extra:  req.Header.Get("X-Test-Header"),
		mod:    req.Header.Get("X-Custom-Auth"),
	})
	t.mu.Unlock()

	body := t.body
	if t.bodies != nil {
		if b, ok := t.bodies[req.URL.Path]; ok {
			body = b
		}
	}
	if body == nil {
		body = []byte(`{}`)
	}
	code := t.code
	if code == 0 {
		code = http.StatusOK
	}
	return &http.Response{
		StatusCode: code,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    req,
	}, nil
}

func (t *captureTripper) last() capturedReq {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mu.reqs[len(t.mu.reqs)-1]
}

func (t *captureTripper) recorded() []capturedReq {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]capturedReq, len(t.mu.reqs))
	copy(out, t.mu.reqs)
	return out
}

func newInternalReqTransport(
	t *testing.T, rec *recordingSigner, header http.Header, rt http.RoundTripper,
) (*Client, *url.URL, *Connection) {
	t.Helper()
	u, err := url.Parse("http://node1:9200")
	require.NoError(t, err)
	conn := &Connection{
		URL:       u,
		URLString: u.String(),
		Name:      "node1",
		seed:      true,
	}
	var sigIface signer.Signer
	if rec != nil {
		sigIface = rec
	}
	tp := &Client{
		urls:               []*url.URL{u},
		transport:          rt,
		signer:             sigIface,
		header:             header,
		userAgent:          "opensearch-go-test",
		healthCheckTimeout: time.Second,
		ctx:                t.Context(),
	}
	tp.mu.connectionPool = newSingleServerPool(conn, nil)
	return tp, u, conn
}

func TestPrepareInternalRequest(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("http://node1:9200")
	require.NoError(t, err)

	tests := []struct {
		name      string
		newTP     func() (*Client, *recordingSigner)
		path      string
		cancelCtx bool
		modifier  func(*http.Request)
		wantErr   []string
		wantErrIs error
		wantAuth  string
		wantUA    string
		wantHost  string
		wantExtra string
		wantUser  string
		wantPass  string
		wantPaths []string
	}{
		{
			name: "nil signer is a no-op and still sets UA",
			newTP: func() (*Client, *recordingSigner) {
				return &Client{userAgent: "ua/1"}, nil
			},
			wantUA:   "ua/1",
			wantHost: "node1:9200",
		},
		{
			name: "signer stamp and global header reach the request",
			newTP: func() (*Client, *recordingSigner) {
				sig := &recordingSigner{}
				return &Client{
					userAgent: "ua/1",
					signer:    sig,
					header:    http.Header{"X-Test-Header": []string{"from-config"}},
				}, sig
			},
			path:      "/_nodes/http",
			wantAuth:  testSigV4Auth,
			wantUA:    "ua/1",
			wantHost:  "node1:9200",
			wantExtra: "from-config",
			wantPaths: []string{"/_nodes/http"},
		},
		{
			name: "modifier runs before signing so its headers are visible to the signer",
			newTP: func() (*Client, *recordingSigner) {
				sig := &recordingSigner{}
				return &Client{userAgent: "ua/1", signer: sig}, sig
			},
			modifier:  func(r *http.Request) { r.Header.Set("X-Test-Header", "from-modifier") },
			wantAuth:  testSigV4Auth,
			wantUA:    "ua/1",
			wantHost:  "node1:9200",
			wantExtra: "from-modifier",
			wantPaths: []string{"/"},
		},
		{
			name: "signer error is wrapped and the request is not left half-decorated silently",
			newTP: func() (*Client, *recordingSigner) {
				return &Client{signer: failingSigner(errors.New("sts timeout"))}, nil
			},
			wantErr: []string{"failed to sign request", "sts timeout"},
		},
		{
			name: "basic auth still applies when no signer is configured",
			newTP: func() (*Client, *recordingSigner) {
				return &Client{username: "admin", password: "secret", userAgent: "ua/1"}, nil
			},
			wantUA:   "ua/1",
			wantHost: "node1:9200",
			wantUser: "admin",
			wantPass: "secret",
		},
		{
			name: "Config.Header Authorization wins over basic auth",
			newTP: func() (*Client, *recordingSigner) {
				return &Client{
					username:  "admin",
					password:  "secret",
					userAgent: "ua/1",
					header:    http.Header{headerAuthorization: []string{"Bearer from-config"}},
				}, nil
			},
			wantAuth: "Bearer from-config",
			wantUA:   "ua/1",
			wantHost: "node1:9200",
		},
		{
			// The helper itself does not consult the request context -- that
			// is the signer's job -- but a cancelled context must still reach
			// the signer so awsv2 can abort credential retrieval.
			name: "cancelled request context reaches the signer",
			newTP: func() (*Client, *recordingSigner) {
				return &Client{signer: contextAwareSigner{}, userAgent: "ua/1"}, nil
			},
			cancelCtx: true,
			wantErrIs: context.Canceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, sig := tt.newTP()

			path := tt.path
			if path == "" {
				path = "/"
			}
			ctx := t.Context()
			if tt.cancelCtx {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
			require.NoError(t, err)

			err = c.prepareInternalRequest(u, req, tt.modifier)
			if len(tt.wantErr) > 0 || tt.wantErrIs != nil {
				if tt.wantErrIs != nil {
					require.ErrorIs(t, err, tt.wantErrIs)
				} else {
					require.Error(t, err)
				}
				for _, substr := range tt.wantErr {
					require.ErrorContains(t, err, substr)
				}
				return
			}
			require.NoError(t, err)

			if tt.wantUser != "" {
				user, pass, ok := req.BasicAuth()
				require.True(t, ok)
				require.Equal(t, tt.wantUser, user)
				require.Equal(t, tt.wantPass, pass)
			} else {
				require.Equal(t, tt.wantAuth, req.Header.Get(headerAuthorization))
			}
			if tt.wantUA != "" {
				require.Equal(t, tt.wantUA, req.Header.Get("User-Agent"))
			}
			if tt.wantHost != "" {
				require.Equal(t, tt.wantHost, req.URL.Host)
			}
			if tt.wantExtra != "" {
				require.Equal(t, tt.wantExtra, req.Header.Get("X-Test-Header"))
			}
			if sig != nil {
				if tt.wantExtra != "" {
					got := sig.recorded()
					require.NotEmpty(t, got)
					require.Equal(t, tt.wantExtra, got[0].extra)
				}
				if tt.wantPaths != nil {
					require.Equal(t, tt.wantPaths, sig.paths())
				}
			}
		})
	}
}

func TestAuthHeaderOrderMatchesStream(t *testing.T) {
	t.Parallel()

	// Both setReqGlobalHeader and setReqAuth are add-if-absent. stream()
	// applies Config.Header first, then basic auth, so a Config.Header
	// Authorization wins. prepareInternalRequest must use the same order
	// or background pollers and user traffic disagree on which credential
	// is sent.
	basicAuthHeader := func(user, pass string) string {
		req := &http.Request{Header: make(http.Header)}
		req.SetBasicAuth(user, pass)
		return req.Header.Get(headerAuthorization)
	}

	tests := []struct {
		name     string
		username string
		password string
		header   http.Header
		reqAuth  string
		wantAuth string
	}{
		{
			name:     "Config.Header Authorization wins over basic auth",
			username: "admin",
			password: "secret",
			header:   http.Header{headerAuthorization: []string{"Bearer from-config"}},
			wantAuth: "Bearer from-config",
		},
		{
			name:     "basic auth applies when Config.Header has no Authorization",
			username: "admin",
			password: "secret",
			wantAuth: basicAuthHeader("admin", "secret"),
		},
		{
			name:     "Config.Header Authorization applies without basic auth",
			header:   http.Header{headerAuthorization: []string{"Bearer from-config"}},
			wantAuth: "Bearer from-config",
		},
		{
			name:     "request Authorization is left intact",
			username: "admin",
			password: "secret",
			header:   http.Header{headerAuthorization: []string{"Bearer from-config"}},
			reqAuth:  "Bearer from-request",
			wantAuth: "Bearer from-request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			u, err := url.Parse("http://node1:9200")
			require.NoError(t, err)

			newPrepared := func() *http.Request {
				t.Helper()
				req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
				require.NoError(t, err)
				if tt.reqAuth != "" {
					req.Header.Set(headerAuthorization, tt.reqAuth)
				}
				return req
			}

			header := tt.header.Clone()
			prepareTP := &Client{
				username:  tt.username,
				password:  tt.password,
				header:    header,
				userAgent: "ua/1",
			}
			prepReq := newPrepared()
			require.NoError(t, prepareTP.prepareInternalRequest(u, prepReq, nil))
			gotPrepare := prepReq.Header.Get(headerAuthorization)

			rt := &captureTripper{}
			streamTP, _, _ := newInternalReqTransport(t, nil, header, rt)
			streamTP.username = tt.username
			streamTP.password = tt.password
			streamReq := newPrepared()
			res, err := streamTP.Stream(streamReq)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			gotStream := rt.last().auth

			require.Equal(t, tt.wantAuth, gotPrepare, "prepareInternalRequest")
			require.Equal(t, tt.wantAuth, gotStream, "stream")
			require.Equal(t, gotPrepare, gotStream, "prepareInternalRequest and stream must not drift")
		})
	}
}

func TestBackgroundPollersSignRequests(t *testing.T) {
	t.Parallel()

	rootBody := []byte(validOpenSearchRootResponse())
	healthBody := []byte(validClusterHealthResponse())
	nodesHTTPBody := []byte(`{
		"_nodes": {"total": 1, "successful": 1, "failed": 0},
		"cluster_name": "test",
		"nodes": {
			"n1": {
				"name": "n1",
				"roles": ["data", "ingest", "cluster_manager"],
				"http": {"publish_address": "127.0.0.1:9200"}
			}
		}
	}`)
	catShardsBody := []byte(`[{"index":"idx","shard":"0","prirep":"p","state":"STARTED","node":"n1"}]`)
	routingMetaBody := []byte(`{"metadata":{"indices":{"idx":{"routing_num_shards":640,"settings":{"index":{"number_of_shards":"5"}}}}}}`)
	hardwareBody := []byte(`{"nodes":{"n1":{"os":{"allocated_processors":8},"thread_pool":{"search":{"max":13}}}}}`)
	nodeStatsBody := []byte(`{"nodes":{"n1":{"jvm":{"mem":{"heap_used_percent":10}},"breakers":{},"thread_pool":{}}}}`)

	bodies := map[string][]byte{
		"/":                                  rootBody,
		"/_cluster/health":                   healthBody,
		"/_nodes/http":                       nodesHTTPBody,
		"/_cat/shards":                       catShardsBody,
		"/_cluster/state/metadata/idx":       routingMetaBody,
		"/_nodes/_local/http,os,thread_pool": hardwareBody,
		"/_nodes/_local/stats/jvm,breaker,thread_pool": nodeStatsBody,
	}

	type poller struct {
		name     string
		path     string
		modifier bool
		// swallows is true when the poller logs a prepare/sign error and
		// returns no error to the caller (currently only node stats).
		swallows bool
		run      func(t *testing.T, tp *Client, u *url.URL, conn *Connection) error
	}

	pollers := []poller{
		{
			name:     "baseline GET /",
			path:     "/",
			modifier: true,
			run: func(t *testing.T, tp *Client, u *url.URL, _ *Connection) error {
				t.Helper()
				res, err := tp.baselineHealthCheck(t.Context(), u, tp.healthCheckRequestModifier)
				if res != nil {
					_ = res.Body.Close()
				}
				return err
			},
		},
		{
			name:     "hardware info",
			path:     "/_nodes/_local/http,os,thread_pool",
			modifier: true,
			run: func(t *testing.T, tp *Client, u *url.URL, conn *Connection) error {
				t.Helper()
				conn.setLifecycleBit(lcNeedsHardware)
				res, err := tp.hardwareInfoHealthCheck(t.Context(), conn, u, tp.healthCheckRequestModifier)
				if res != nil {
					_ = res.Body.Close()
				}
				return err
			},
		},
		{
			name:     "cluster health",
			path:     "/_cluster/health",
			modifier: true,
			run: func(t *testing.T, tp *Client, u *url.URL, _ *Connection) error {
				t.Helper()
				_, _, err := tp.fetchClusterHealth(t.Context(), u, tp.healthCheckRequestModifier)
				return err
			},
		},
		{
			name:     "node stats",
			path:     "/_nodes/_local/stats/jvm,breaker,thread_pool",
			modifier: true,
			swallows: true,
			run: func(t *testing.T, tp *Client, _ *url.URL, conn *Connection) error {
				t.Helper()
				// ok is about search-pool samples, not about whether the
				// request was prepared and dispatched. A sign failure is
				// swallowed (logged) and still returns ok=false.
				// fetchAndEvaluateNodeStats uses Transport.ctx; it does not
				// take a caller context.
				tp.fetchAndEvaluateNodeStats(conn, nil)
				return nil
			},
		},
		{
			name: "nodes info",
			path: "/_nodes/http",
			run: func(t *testing.T, tp *Client, _ *url.URL, _ *Connection) error {
				t.Helper()
				_, err := tp.getNodesInfo(t.Context())
				return err
			},
		},
		{
			name: "shard placement",
			path: "/_cat/shards",
			run: func(t *testing.T, tp *Client, _ *url.URL, _ *Connection) error {
				t.Helper()
				_, err := tp.getShardPlacement(t.Context())
				return err
			},
		},
		{
			name: "routing meta",
			path: "/_cluster/state/metadata/idx",
			run: func(t *testing.T, tp *Client, _ *url.URL, _ *Connection) error {
				t.Helper()
				_, err := tp.getRoutingMeta(t.Context(), []string{"idx"})
				return err
			},
		},
	}

	modes := []struct {
		name    string
		signer  func() *recordingSigner
		header  http.Header
		wantErr bool
	}{
		{
			name:   "each background path is signed before RoundTrip",
			signer: func() *recordingSigner { return &recordingSigner{} },
			header: http.Header{"X-Test-Header": []string{"from-config"}},
		},
		{
			name:   "nil signer still dispatches (basic-auth / no-auth clusters)",
			signer: func() *recordingSigner { return nil },
		},
		{
			name:    "signer error aborts before RoundTrip",
			signer:  func() *recordingSigner { return failingSigner(errors.New("no credentials")) },
			wantErr: true,
		},
	}

	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			t.Parallel()
			if mode.wantErr {
				// Covers the fetchAndEvaluateNodeStats prepare-failure log.
				enableTestDebugLogger(t)
			}

			for _, p := range pollers {
				t.Run(p.name, func(t *testing.T) {
					t.Parallel()
					sig := mode.signer()
					rt := &captureTripper{bodies: bodies}
					tp, u, conn := newInternalReqTransport(t, sig, mode.header.Clone(), rt)
					if mode.header != nil {
						tp.healthCheckRequestModifier = func(r *http.Request) {
							r.Header.Set("X-Custom-Auth", "modifier")
						}
					}

					err := p.run(t, tp, u, conn)
					if mode.wantErr {
						if !p.swallows {
							require.Error(t, err)
							require.ErrorContains(t, err, "failed to sign request")
						} else {
							require.NoError(t, err)
						}
						require.Empty(t, rt.recorded(), "unsigned request must not leave the process")
						require.Equal(t, []string{p.path}, sig.paths())
						return
					}
					require.NoError(t, err)

					got := rt.last()
					require.Equal(t, p.path, got.path)
					require.Equal(t, "opensearch-go-test", got.ua)
					if sig != nil {
						require.Equal(t, testSigV4Auth, got.auth)
						require.Equal(t, []string{p.path}, sig.paths())
					} else {
						require.Empty(t, got.auth)
					}
					if mode.header != nil {
						require.Equal(t, "from-config", got.extra)
						if p.modifier {
							require.Equal(t, "modifier", got.mod)
						} else {
							require.Empty(t, got.mod)
						}
					}
				})
			}
		})
	}
}

// contextAwareSigner fails if the request context is already cancelled,
// matching what signer/awsv2 does once SignRequest uses r.Context().
type contextAwareSigner struct{}

func (contextAwareSigner) SignRequest(req *http.Request) error { return req.Context().Err() }
func (contextAwareSigner) OverrideSigningPort(uint16)          {}

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

	"github.com/opensearch-project/opensearch-go/v5/signer"
)

// recordingSigner records every request it is asked to sign and stamps
// Authorization so the RoundTripper can assert the stamp made it onto
// the wire. err, if set, is returned after the request is recorded.
type recordingSigner struct {
	mu   sync.Mutex
	reqs []recordedSign
	err  error
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
	s.reqs = append(s.reqs, recordedSign{
		method: req.Method,
		path:   req.URL.Path,
		ua:     req.Header.Get("User-Agent"),
		extra:  req.Header.Get("X-Test-Header"),
	})
	if s.err != nil {
		return s.err
	}
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 signed")
	return nil
}

func (s *recordingSigner) OverrideSigningPort(uint16) {}

func (s *recordingSigner) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.reqs))
	for i, r := range s.reqs {
		out[i] = r.path
	}
	return out
}

// captureTripper records the request that actually left the process and
// returns a canned response. Header values are snapshotted at RoundTrip
// time so a later mutation of the original request cannot hide a miss.
type captureTripper struct {
	mu     sync.Mutex
	reqs   []capturedReq
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
	t.reqs = append(t.reqs, capturedReq{
		method: req.Method,
		path:   req.URL.Path,
		auth:   req.Header.Get("Authorization"),
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
	return t.reqs[len(t.reqs)-1]
}

func newInternalReqTransport(
	t *testing.T, rec *recordingSigner, header http.Header, rt http.RoundTripper,
) (*Transport, *url.URL, *Connection) {
	t.Helper()
	u, err := url.Parse("http://node1:9200")
	require.NoError(t, err)
	conn := &Connection{
		URL:       u,
		URLString: u.String(),
		hostPort:  "http://node1:9200",
		Name:      "node1",
		seed:      true,
	}
	var sigIface signer.Signer
	if rec != nil {
		sigIface = rec
	}
	tp := &Transport{
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

	t.Run("nil signer is a no-op and still sets UA", func(t *testing.T) {
		t.Parallel()
		c := &Transport{userAgent: "ua/1"}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		require.NoError(t, err)

		require.NoError(t, c.prepareInternalRequest(u, req, nil))
		require.Empty(t, req.Header.Get("Authorization"))
		require.Equal(t, "ua/1", req.Header.Get("User-Agent"))
		require.Equal(t, "node1:9200", req.URL.Host)
	})

	t.Run("signer stamp and global header reach the request", func(t *testing.T) {
		t.Parallel()
		sig := &recordingSigner{}
		c := &Transport{
			userAgent: "ua/1",
			signer:    sig,
			header:    http.Header{"X-Test-Header": []string{"from-config"}},
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/_nodes/http", nil)
		require.NoError(t, err)

		require.NoError(t, c.prepareInternalRequest(u, req, nil))
		require.Equal(t, "AWS4-HMAC-SHA256 signed", req.Header.Get("Authorization"))
		require.Equal(t, "from-config", req.Header.Get("X-Test-Header"))
		require.Equal(t, []string{"/_nodes/http"}, sig.paths())
	})

	t.Run("modifier runs before signing so its headers are visible to the signer", func(t *testing.T) {
		t.Parallel()
		sig := &recordingSigner{}
		c := &Transport{userAgent: "ua/1", signer: sig}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		require.NoError(t, err)

		require.NoError(t, c.prepareInternalRequest(u, req, func(r *http.Request) {
			r.Header.Set("X-Test-Header", "from-modifier")
		}))
		require.Equal(t, "from-modifier", req.Header.Get("X-Test-Header"))
		require.Equal(t, "from-modifier", sig.reqs[0].extra)
		require.Equal(t, "AWS4-HMAC-SHA256 signed", req.Header.Get("Authorization"))
	})

	t.Run("signer error is wrapped and the request is not left half-decorated silently", func(t *testing.T) {
		t.Parallel()
		c := &Transport{signer: &recordingSigner{err: errors.New("sts timeout")}}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		require.NoError(t, err)

		err = c.prepareInternalRequest(u, req, nil)
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to sign request")
		require.ErrorContains(t, err, "sts timeout")
	})

	t.Run("basic auth still applies when no signer is configured", func(t *testing.T) {
		t.Parallel()
		c := &Transport{username: "admin", password: "secret", userAgent: "ua/1"}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		require.NoError(t, err)

		require.NoError(t, c.prepareInternalRequest(u, req, nil))
		user, pass, ok := req.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "admin", user)
		require.Equal(t, "secret", pass)
	})
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

	t.Run("each background path is signed before RoundTrip", func(t *testing.T) {
		t.Parallel()
		sig := &recordingSigner{}
		rt := &captureTripper{bodies: map[string][]byte{
			"/":                                  rootBody,
			"/_cluster/health":                   healthBody,
			"/_nodes/http":                       nodesHTTPBody,
			"/_cat/shards":                       catShardsBody,
			"/_cluster/state/metadata/idx":       routingMetaBody,
			"/_nodes/_local/http,os,thread_pool": hardwareBody,
			"/_nodes/_local/stats/jvm,breaker,thread_pool": nodeStatsBody,
		}}
		header := http.Header{"X-Test-Header": []string{"from-config"}}
		tp, u, conn := newInternalReqTransport(t, sig, header, rt)
		tp.healthCheckRequestModifier = func(r *http.Request) {
			r.Header.Set("X-Custom-Auth", "modifier")
		}

		// GET /
		res, err := tp.baselineHealthCheck(t.Context(), u, tp.healthCheckRequestModifier)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		got := rt.last()
		require.Equal(t, "/", got.path)
		require.Equal(t, "AWS4-HMAC-SHA256 signed", got.auth)
		require.Equal(t, "opensearch-go-test", got.ua)
		require.Equal(t, "from-config", got.extra)
		require.Equal(t, "modifier", got.mod)

		// GET /_nodes/_local/http,os,thread_pool
		conn.setLifecycleBit(lcNeedsHardware)
		res, err = tp.hardwareInfoHealthCheck(t.Context(), conn, u, tp.healthCheckRequestModifier)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		got = rt.last()
		require.Equal(t, "/_nodes/_local/http,os,thread_pool", got.path)
		require.Equal(t, "AWS4-HMAC-SHA256 signed", got.auth)

		// GET /_cluster/health?local=true
		_, status, err := tp.fetchClusterHealth(t.Context(), u, tp.healthCheckRequestModifier)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status)
		got = rt.last()
		require.Equal(t, "/_cluster/health", got.path)
		require.Equal(t, "AWS4-HMAC-SHA256 signed", got.auth)

		// GET /_nodes/_local/stats/...
		tp.fetchAndEvaluateNodeStats(conn, nil)
		got = rt.last()
		require.Equal(t, "/_nodes/_local/stats/jvm,breaker,thread_pool", got.path)
		require.Equal(t, "AWS4-HMAC-SHA256 signed", got.auth)
		require.Equal(t, "modifier", got.mod)

		// GET /_nodes/http
		_, err = tp.getNodesInfo(t.Context())
		require.NoError(t, err)
		got = rt.last()
		require.Equal(t, "/_nodes/http", got.path)
		require.Equal(t, "AWS4-HMAC-SHA256 signed", got.auth)

		// GET /_cat/shards
		_, err = tp.getShardPlacement(t.Context())
		require.NoError(t, err)
		got = rt.last()
		require.Equal(t, "/_cat/shards", got.path)
		require.Equal(t, "AWS4-HMAC-SHA256 signed", got.auth)

		// GET /_cluster/state/metadata/...
		_, err = tp.getRoutingMeta(t.Context(), []string{"idx"})
		require.NoError(t, err)
		got = rt.last()
		require.Equal(t, "/_cluster/state/metadata/idx", got.path)
		require.Equal(t, "AWS4-HMAC-SHA256 signed", got.auth)

		require.Equal(t, []string{
			"/",
			"/_nodes/_local/http,os,thread_pool",
			"/_cluster/health",
			"/_nodes/_local/stats/jvm,breaker,thread_pool",
			"/_nodes/http",
			"/_cat/shards",
			"/_cluster/state/metadata/idx",
		}, sig.paths())
	})

	t.Run("nil signer still dispatches (basic-auth / no-auth clusters)", func(t *testing.T) {
		t.Parallel()
		rt := &captureTripper{body: rootBody}
		tp, u, _ := newInternalReqTransport(t, nil, nil, rt)

		res, err := tp.baselineHealthCheck(t.Context(), u, nil)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		got := rt.last()
		require.Equal(t, "/", got.path)
		require.Empty(t, got.auth)
		require.Equal(t, "opensearch-go-test", got.ua)
	})

	t.Run("signer error aborts before RoundTrip", func(t *testing.T) {
		t.Parallel()
		sig := &recordingSigner{err: errors.New("no credentials")}
		rt := &captureTripper{body: rootBody}
		tp, u, _ := newInternalReqTransport(t, sig, nil, rt)

		res, err := tp.baselineHealthCheck(t.Context(), u, nil)
		if res != nil && res.Body != nil {
			require.NoError(t, res.Body.Close())
		}
		require.Error(t, err)
		require.Nil(t, res)
		require.ErrorIs(t, err, errHealthCheckFailed)
		require.ErrorContains(t, err, "failed to sign request")
		require.Empty(t, rt.reqs, "unsigned request must not leave the process")
		require.Equal(t, []string{"/"}, sig.paths())
	})
}

func TestPrepareInternalRequest_CancelledContext(t *testing.T) {
	t.Parallel()

	// The helper itself does not consult the request context -- that is
	// the signer's job -- but a cancelled context must still reach the
	// signer so awsv2 can abort credential retrieval. A signer that
	// surfaces ctx.Err() is how we observe the hand-off.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	sig := &contextAwareSigner{}
	c := &Transport{signer: sig, userAgent: "ua/1"}
	u, err := url.Parse("http://node1:9200")
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	require.NoError(t, err)

	err = c.prepareInternalRequest(u, req, nil)
	require.ErrorIs(t, err, context.Canceled)
}

// contextAwareSigner fails if the request context is already cancelled,
// matching what signer/awsv2 does once SignRequest uses r.Context().
type contextAwareSigner struct{}

func (contextAwareSigner) SignRequest(req *http.Request) error { return req.Context().Err() }
func (contextAwareSigner) OverrideSigningPort(uint16)          {}

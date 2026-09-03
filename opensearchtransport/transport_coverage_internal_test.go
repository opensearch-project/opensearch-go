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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

func TestSetReqURL(t *testing.T) {
	t.Parallel()

	t.Run("simple URL", func(t *testing.T) {
		t.Parallel()
		c := &Transport{}
		u, _ := url.Parse("https://node1:9200")
		req, _ := http.NewRequest(http.MethodGet, "/_search", nil)
		c.setReqURL(u, req)

		require.Equal(t, "https", req.URL.Scheme)
		require.Equal(t, "node1:9200", req.URL.Host)
		require.Equal(t, "/_search", req.URL.Path)
	})

	t.Run("URL with base path", func(t *testing.T) {
		t.Parallel()
		c := &Transport{}
		u, _ := url.Parse("https://node1:9200/prefix")
		req, _ := http.NewRequest(http.MethodGet, "/_search", nil)
		c.setReqURL(u, req)

		require.Equal(t, "/prefix/_search", req.URL.Path)
	})

	t.Run("URL with trailing slash base path", func(t *testing.T) {
		t.Parallel()
		c := &Transport{}
		u, _ := url.Parse("https://node1:9200/prefix/")
		req, _ := http.NewRequest(http.MethodGet, "/_search", nil)
		c.setReqURL(u, req)

		require.Equal(t, "/prefix/_search", req.URL.Path)
	})

	t.Run("URL with just slash path", func(t *testing.T) {
		t.Parallel()
		c := &Transport{}
		u, _ := url.Parse("https://node1:9200/")
		req, _ := http.NewRequest(http.MethodGet, "/_search", nil)
		c.setReqURL(u, req)

		require.Equal(t, "/_search", req.URL.Path)
	})

	t.Run("URL with empty path", func(t *testing.T) {
		t.Parallel()
		c := &Transport{}
		u, _ := url.Parse("https://node1:9200")
		req, _ := http.NewRequest(http.MethodGet, "/my-index/_doc/1", nil)
		c.setReqURL(u, req)

		require.Equal(t, "/my-index/_doc/1", req.URL.Path)
	})

	t.Run("deep path prefix", func(t *testing.T) {
		t.Parallel()
		c := &Transport{}
		u, _ := url.Parse("http://proxy:8080/api/v1/opensearch")
		req, _ := http.NewRequest(http.MethodGet, "/_cat/indices", nil)
		c.setReqURL(u, req)

		require.Equal(t, "/api/v1/opensearch/_cat/indices", req.URL.Path)
	})
}

// TestSetReqURLRestoredPath rewrites one *http.Request repeatedly, the shape
// stream() uses across retries: [restoreReqPath] then setReqURL, once per
// attempt. nodeURLs supplies the connection for each attempt in order, so a
// prefixed seed followed by a prefix-less discovered node is a single row.
func TestSetReqURLRestoredPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		nodeURLs     []string
		reqURL       string
		wantPaths    []string
		wantRawPaths []string // asserted only when set; Path carries the decoded form
	}{
		{
			name:      "the same prefixed node is stable across attempts",
			nodeURLs:  []string{"https://node1:9200/prefix", "https://node1:9200/prefix", "https://node1:9200/prefix"},
			reqURL:    "/_search",
			wantPaths: []string{"/prefix/_search", "/prefix/_search", "/prefix/_search"},
		},
		{
			name:      "switching to a prefix-less connection drops the prefix",
			nodeURLs:  []string{"https://proxy:9200/prefix", "https://node:9200"},
			reqURL:    "/_search",
			wantPaths: []string{"/prefix/_search", "/_search"},
		},
		{
			name:         "percent-encoded RawPath is prepended from the original",
			nodeURLs:     []string{"https://node1:9200/prefix", "https://node1:9200/prefix"},
			reqURL:       "/idx/_doc/a%2Fb",
			wantPaths:    []string{"/prefix/idx/_doc/a/b", "/prefix/idx/_doc/a/b"},
			wantRawPaths: []string{"/prefix/idx/_doc/a%2Fb", "/prefix/idx/_doc/a%2Fb"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &Transport{}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, tt.reqURL, nil)
			require.NoError(t, err)
			origPath, origRawPath := req.URL.Path, req.URL.RawPath

			for i, nodeURL := range tt.nodeURLs {
				u, err := url.Parse(nodeURL)
				require.NoError(t, err)

				restoreReqPath(req, origPath, origRawPath)
				c.setReqURL(u, req)

				require.Equal(t, tt.wantPaths[i], req.URL.Path)
				require.Equal(t, u.Host, req.URL.Host)
				if tt.wantRawPaths != nil {
					require.Equal(t, tt.wantRawPaths[i], req.URL.RawPath)
				}
			}
		})
	}
}

// TestStreamRetryPathPrefix is the user-visible form of setReqURL rewriting
// Path in place: stream reuses one *http.Request, default MaxRetries is > 0,
// and a reverse-proxy prefix is a supported address shape. Every hop -- retry,
// Route(), seed fallback, percent-encoded RawPath -- must start from the
// caller path so setReqURL prepends once. The cases share that fixture; they
// differ only in which path (wire, Route, EscapedPath) they pin.
func TestStreamRetryPathPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		nodeURLs   []string // one connection per attempt, in order; the last repeats
		reqURL     string
		maxRetries int
		failAfter  int32 // recordingRouter: succeed N times, then ErrNoConnections
		statuses   []int // RoundTrip codes in order; the last value repeats
		wantStatus int
		wantWire   []string
		wantRoute  []string // recording Route() implies a router; empty means pool routing
		escaped    bool     // record EscapedPath instead of Path
	}{
		{
			name:       "retries do not stack a connection prefix",
			nodeURLs:   []string{"https://node1:9200/prefix"},
			reqURL:     "/_search",
			maxRetries: 2,
			statuses:   []int{http.StatusBadGateway},
			wantStatus: http.StatusBadGateway,
			wantWire:   []string{"/prefix/_search", "/prefix/_search", "/prefix/_search"},
		},
		{
			name:       "Route sees the original path on each attempt",
			nodeURLs:   []string{"https://node1:9200/prefix"},
			reqURL:     "/_search",
			maxRetries: 2,
			statuses:   []int{http.StatusBadGateway},
			wantStatus: http.StatusBadGateway,
			wantRoute:  []string{"/_search", "/_search", "/_search"},
		},
		{
			name:       "a prefix-less node after a prefixed one drops the prefix",
			nodeURLs:   []string{"https://proxy:9200/prefix", "https://node:9200"},
			reqURL:     "/_search",
			maxRetries: 1,
			statuses:   []int{http.StatusBadGateway, http.StatusOK},
			wantStatus: http.StatusOK,
			wantWire:   []string{"/prefix/_search", "/_search"},
			wantRoute:  []string{"/_search", "/_search"},
		},
		{
			name:       "retry then seed fallback does not stack prefix",
			nodeURLs:   []string{"http://seed-node:9200/prefix"},
			reqURL:     "/_search",
			maxRetries: 1,
			failAfter:  1,
			statuses:   []int{http.StatusBadGateway, http.StatusOK},
			wantStatus: http.StatusOK,
			wantWire:   []string{"/prefix/_search", "/prefix/_search"},
			wantRoute:  []string{"/_search", "/_search"},
		},
		{
			name:       "percent-encoded RawPath is restored on retry",
			nodeURLs:   []string{"https://node1:9200/prefix"},
			reqURL:     "/idx/_doc/a%2Fb",
			maxRetries: 1,
			statuses:   []int{http.StatusBadGateway},
			wantStatus: http.StatusBadGateway,
			escaped:    true,
			wantWire:   []string{"/prefix/idx/_doc/a%2Fb", "/prefix/idx/_doc/a%2Fb"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			urls := make([]*url.URL, 0, len(tt.nodeURLs))
			for _, nodeURL := range tt.nodeURLs {
				u, err := url.Parse(nodeURL)
				require.NoError(t, err)
				urls = append(urls, u)
			}

			var router *recordingRouter
			cfg := Config{
				URLs:              urls,
				MaxRetries:        tt.maxRetries,
				NodeStatsInterval: -1,
				HealthCheck:       NoOpHealthCheck,
			}
			if len(tt.wantRoute) > 0 {
				conns := make([]*Connection, 0, len(urls))
				for _, u := range urls {
					conns = append(conns, &Connection{URL: u, URLString: u.String(), hostPort: hostPrefixOf(u)})
				}
				router = &recordingRouter{conns: conns, failAfter: tt.failAfter}
				cfg.Router = router
			}

			var (
				mu struct {
					sync.Mutex
					paths []string
				}
				n atomic.Int32
			)
			cfg.Transport = mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
				path := req.URL.Path
				if tt.escaped {
					path = req.URL.EscapedPath()
				}
				mu.Lock()
				mu.paths = append(mu.paths, path)
				mu.Unlock()

				i := int(n.Add(1))
				code := tt.statuses[len(tt.statuses)-1]
				if i <= len(tt.statuses) {
					code = tt.statuses[i-1]
				}
				return &http.Response{StatusCode: code, Body: http.NoBody}, nil
			})

			tp, err := New(cfg)
			require.NoError(t, err)
			t.Cleanup(func() { _ = tp.Close() })
			if tt.failAfter > 0 {
				require.NotNil(t, tp.seedFallbackPool)
			}

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, tt.reqURL, nil)
			require.NoError(t, err)
			res, err := tp.Stream(req)
			require.NoError(t, err)
			require.NotNil(t, res)
			require.Equal(t, tt.wantStatus, res.StatusCode)
			if res.Body != nil {
				res.Body.Close()
			}

			mu.Lock()
			got := append([]string(nil), mu.paths...)
			mu.Unlock()
			if tt.wantWire != nil {
				require.Equal(t, tt.wantWire, got)
			}
			if tt.wantRoute != nil {
				require.Equal(t, tt.wantRoute, router.paths())
			}
		})
	}
}

func TestSetReqAuth(t *testing.T) {
	t.Parallel()

	t.Run("auth from URL userinfo", func(t *testing.T) {
		t.Parallel()
		c := &Transport{}
		u, _ := url.Parse("https://admin:password@node1:9200")
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		c.setReqAuth(u, req)

		user, pass, ok := req.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "admin", user)
		require.Equal(t, "password", pass)
	})

	t.Run("auth from client credentials", func(t *testing.T) {
		t.Parallel()
		c := &Transport{username: "admin", password: "secret"}
		u, _ := url.Parse("https://node1:9200")
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		c.setReqAuth(u, req)

		user, pass, ok := req.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "admin", user)
		require.Equal(t, "secret", pass)
	})

	t.Run("skips when Authorization header present", func(t *testing.T) {
		t.Parallel()
		c := &Transport{username: "admin", password: "secret"}
		u, _ := url.Parse("https://node1:9200")
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer token")
		c.setReqAuth(u, req)

		require.Equal(t, "Bearer token", req.Header.Get("Authorization"))
	})

	t.Run("no auth when no credentials", func(t *testing.T) {
		t.Parallel()
		c := &Transport{}
		u, _ := url.Parse("https://node1:9200")
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		c.setReqAuth(u, req)

		require.Empty(t, req.Header.Get("Authorization"))
	})

	t.Run("URL userinfo takes precedence over client creds", func(t *testing.T) {
		t.Parallel()
		c := &Transport{username: "client-user", password: "client-pass"}
		u, _ := url.Parse("https://url-user:url-pass@node1:9200")
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		c.setReqAuth(u, req)

		user, pass, ok := req.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "url-user", user)
		require.Equal(t, "url-pass", pass)
	})

	t.Run("partial client creds do not set auth", func(t *testing.T) {
		t.Parallel()
		c := &Transport{username: "admin"} // no password
		u, _ := url.Parse("https://node1:9200")
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		c.setReqAuth(u, req)

		_, _, ok := req.BasicAuth()
		require.False(t, ok)
	})

	t.Run("API key sets Authorization header", func(t *testing.T) {
		t.Parallel()
		dummyApiKey := "dGVzdGlkOnRlc3RrZXk="
		c := &Transport{apiKey: dummyApiKey}
		u, _ := url.Parse("https://node1:9200")
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		c.setReqAuth(u, req)

		require.Equal(t, fmt.Sprintf("ApiKey %s", dummyApiKey), req.Header.Get("Authorization"))
	})

	t.Run("API key takes precedence over username/password", func(t *testing.T) {
		t.Parallel()
		dummyApiKey := "dGVzdGlkOnRlc3RrZXk="
		c := &Transport{apiKey: dummyApiKey, username: "admin", password: "secret"}
		u, _ := url.Parse("https://node1:9200")
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		c.setReqAuth(u, req)

		require.Equal(t, fmt.Sprintf("ApiKey %s", dummyApiKey), req.Header.Get("Authorization"))
		_, _, basicOK := req.BasicAuth()
		require.False(t, basicOK)
	})

	t.Run("URL userinfo takes precedence over API key", func(t *testing.T) {
		t.Parallel()
		dummyApiKey := "dGVzdGlkOnRlc3RrZXk="
		c := &Transport{apiKey: dummyApiKey}
		u, _ := url.Parse("https://url-user:url-pass@node1:9200")
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		c.setReqAuth(u, req)

		user, pass, ok := req.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "url-user", user)
		require.Equal(t, "url-pass", pass)
	})

	t.Run("existing Authorization header not overwritten by API key", func(t *testing.T) {
		t.Parallel()
		dummyApiKey := "dGVzdGlkOnRlc3RrZXk="
		c := &Transport{apiKey: dummyApiKey}
		u, _ := url.Parse("https://node1:9200")
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		existingToken := "some-random-token"
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", existingToken))
		c.setReqAuth(u, req)

		require.Equal(t, fmt.Sprintf("Bearer %s", existingToken), req.Header.Get("Authorization"))
	})
}

func TestSignRequest_Coverage(t *testing.T) {
	t.Parallel()

	t.Run("nil signer returns nil", func(t *testing.T) {
		t.Parallel()
		c := &Transport{}
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		require.NoError(t, c.signRequest(req))
	})

	t.Run("signer adds header", func(t *testing.T) {
		t.Parallel()
		c := &Transport{signer: &mockSigner{SampleKey: "X-Auth", SampleValue: "signed"}}
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		require.NoError(t, c.signRequest(req))
		require.Equal(t, "signed", req.Header.Get("X-Auth"))
	})

	t.Run("signer error propagates", func(t *testing.T) {
		t.Parallel()
		c := &Transport{signer: &mockSigner{ReturnError: true}}
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		err := c.signRequest(req)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid data")
	})
}

func TestClose_Coverage(t *testing.T) {
	t.Parallel()

	t.Run("closes without cancel func", func(t *testing.T) {
		t.Parallel()
		c := &Transport{}
		require.NoError(t, c.Close())
	})

	t.Run("closes with cancel func", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		c := &Transport{
			cancelFunc: cancel,
			ctx:        ctx,
			transport:  http.DefaultTransport,
		}
		require.NoError(t, c.Close())
		require.ErrorIs(t, ctx.Err(), context.Canceled)
	})

	t.Run("closes transport with CloseIdleConnections", func(t *testing.T) {
		t.Parallel()
		closed := false
		c := &Transport{transport: &closeableTransport{fn: func() { closed = true }}}
		require.NoError(t, c.Close())
		require.True(t, closed)
	})
}

// closeableTransport implements http.RoundTripper with CloseIdleConnections.
type closeableTransport struct {
	fn func()
}

func (t *closeableTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

func (t *closeableTransport) CloseIdleConnections() { t.fn() }

func TestDemoteConnectionPoolWithLock(t *testing.T) {
	t.Parallel()

	t.Run("multi pool with ready connections", func(t *testing.T) {
		t.Parallel()
		u, _ := url.Parse("http://node1:9200")
		conn := &Connection{URL: u, Name: "node-1"}

		pool := &multiServerPool{}
		pool.mu.ready = []*Connection{conn}
		pool.mu.activeCount = 1

		c := &Transport{}
		c.mu.connectionPool = pool

		result := c.demoteConnectionPoolWithLock()
		require.NotNil(t, result.connection)
		require.Equal(t, "node-1", result.connection.Name)
	})

	t.Run("multi pool with only dead connections", func(t *testing.T) {
		t.Parallel()
		u, _ := url.Parse("http://dead:9200")
		conn := &Connection{URL: u, Name: "dead-node"}

		pool := &multiServerPool{}
		pool.mu.dead = []*Connection{conn}

		c := &Transport{}
		c.mu.connectionPool = pool

		result := c.demoteConnectionPoolWithLock()
		require.NotNil(t, result.connection)
		require.Equal(t, "dead-node", result.connection.Name)
	})

	t.Run("multi pool with no connections", func(t *testing.T) {
		t.Parallel()
		pool := &multiServerPool{}
		c := &Transport{}
		c.mu.connectionPool = pool

		result := c.demoteConnectionPoolWithLock()
		require.Nil(t, result.connection)
	})

	t.Run("already single pool returns unchanged", func(t *testing.T) {
		t.Parallel()
		u, _ := url.Parse("http://single:9200")
		conn := &Connection{URL: u, Name: "single"}
		pool := &singleServerPool{connection: conn}

		c := &Transport{}
		c.mu.connectionPool = pool

		result := c.demoteConnectionPoolWithLock()
		require.Same(t, pool, result)
	})
}

func TestLogRoundTrip_Coverage(t *testing.T) {
	t.Parallel()

	t.Run("logs with nil response and error", func(t *testing.T) {
		t.Parallel()
		ml := &logCapture{}
		c := &Transport{logger: ml}

		req, _ := http.NewRequest(http.MethodGet, "http://localhost:9200/_search", nil)
		c.logRoundTrip(req, nil, fmt.Errorf("connection refused"), time.Now(), time.Millisecond)

		require.True(t, ml.called)
		require.Error(t, ml.lastErr)
	})

	t.Run("logs with response body enabled", func(t *testing.T) {
		t.Parallel()
		ml := &logCapture{respBodyEnabled: true}
		c := &Transport{logger: ml}

		req, _ := http.NewRequest(http.MethodGet, "http://localhost:9200/_search", nil)
		res := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"hits":{}}`)),
		}
		c.logRoundTrip(req, res, nil, time.Now(), time.Millisecond)

		require.True(t, ml.called)
		// Original body should still be readable after duplicateBody
		data, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Contains(t, string(data), "hits")
	})

	t.Run("logs with http.NoBody response", func(t *testing.T) {
		t.Parallel()
		ml := &logCapture{respBodyEnabled: true}
		c := &Transport{logger: ml}

		req, _ := http.NewRequest(http.MethodGet, "http://localhost:9200/", nil)
		res := &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}
		c.logRoundTrip(req, res, nil, time.Now(), time.Millisecond)

		require.True(t, ml.called)
	})
}

// logCapture is a minimal Logger for testing logRoundTrip.
type logCapture struct {
	called          bool
	lastErr         error
	reqBodyEnabled  bool
	respBodyEnabled bool
}

func (l *logCapture) LogRoundTrip(_ *http.Request, _ *http.Response, err error, _ time.Time, _ time.Duration) error {
	l.called = true
	l.lastErr = err
	return nil
}
func (l *logCapture) RequestBodyEnabled() bool  { return l.reqBodyEnabled }
func (l *logCapture) ResponseBodyEnabled() bool { return l.respBodyEnabled }

func TestPerform_GzipCompression(t *testing.T) {
	t.Parallel()

	seedURL, _ := url.Parse("http://localhost:9200")
	tp, err := New(Config{
		URLs:                []*url.URL{seedURL},
		HealthCheck:         NoOpHealthCheck,
		CompressRequestBody: true,
		DisableRetry:        true,
		NodeStatsInterval:   -1, // Disable stats poller to avoid background requests through mock transport
		Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
			require.Equal(t, "gzip", req.Header.Get("Content-Encoding"))
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK"}, nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })

	body := bytes.NewReader([]byte(`{"query":{"match_all":{}}}`))
	req, _ := http.NewRequest(http.MethodPost, "/_search", io.NopCloser(body))
	res, err := tp.Stream(req)
	require.NoError(t, err)
	require.NotNil(t, res)
	if res.Body != nil {
		res.Body.Close()
	}
}

func TestPerform_MetricsEnabled(t *testing.T) {
	t.Parallel()

	seedURL, _ := url.Parse("http://localhost:9200")
	tp, err := New(Config{
		URLs:              []*url.URL{seedURL},
		HealthCheck:       NoOpHealthCheck,
		DisableRetry:      true,
		NodeStatsInterval: -1, // Disable stats poller to avoid background requests through mock transport
		Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK"}, nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })

	req, _ := http.NewRequest(http.MethodGet, "/test", nil)
	res, err := tp.Stream(req)
	require.NoError(t, err)
	if res != nil && res.Body != nil {
		res.Body.Close()
	}

	require.GreaterOrEqual(t, tp.metrics.requests.Load(), int64(1))
}

func TestPerform_SignError(t *testing.T) {
	t.Parallel()

	seedURL, _ := url.Parse("http://localhost:9200")
	tp, err := New(Config{
		URLs:              []*url.URL{seedURL},
		HealthCheck:       NoOpHealthCheck,
		DisableRetry:      true,
		Signer:            &mockSigner{ReturnError: true},
		NodeStatsInterval: -1, // Disable stats poller to avoid background requests through mock transport
		Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK"}, nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })

	req, _ := http.NewRequest(http.MethodGet, "/test", nil)
	res, err := tp.Stream(req) //nolint:bodyclose // error path
	require.Error(t, err)
	require.Nil(t, res)
	require.Contains(t, err.Error(), "failed to sign request")
}

func TestPerform_TransportError(t *testing.T) {
	t.Parallel()

	seedURL, _ := url.Parse("http://localhost:9200")
	tp, err := New(Config{
		URLs:              []*url.URL{seedURL},
		HealthCheck:       NoOpHealthCheck,
		DisableRetry:      true,
		NodeStatsInterval: -1, // Disable stats poller to avoid background requests through mock transport
		Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })
	tp.seedFallbackDisabled = true
	tp.seedFallbackPool = nil

	req, _ := http.NewRequest(http.MethodGet, "/test", nil)
	res, err := tp.Stream(req) //nolint:bodyclose // error path
	require.Error(t, err)
	require.Nil(t, res)
}

func TestPerform_NetworkErrorRetry(t *testing.T) {
	t.Parallel()

	callCount := 0
	seedURL, _ := url.Parse("http://localhost:9200")
	tp, err := New(Config{
		URLs:              []*url.URL{seedURL},
		HealthCheck:       NoOpHealthCheck,
		MaxRetries:        1,
		NodeStatsInterval: -1, // Disable stats poller to avoid background requests through mock transport
		Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
			callCount++
			if callCount == 1 {
				return nil, &mockNetError{error: fmt.Errorf("network error")}
			}
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK"}, nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })

	req, _ := http.NewRequest(http.MethodGet, "/test", nil)
	res, err := tp.Stream(req)
	require.NoError(t, err)
	require.NotNil(t, res)
	if res.Body != nil {
		res.Body.Close()
	}
	require.Equal(t, 2, callCount)
}

func TestBackoffRetry(t *testing.T) {
	t.Parallel()

	t.Run("success on first attempt", func(t *testing.T) {
		t.Parallel()
		calls := 0
		err := backoffRetry(time.Millisecond, 3, 0, func() error {
			calls++
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, 1, calls)
	})

	t.Run("success on retry", func(t *testing.T) {
		t.Parallel()
		calls := 0
		err := backoffRetry(time.Millisecond, 3, 0, func() error {
			calls++
			if calls < 3 {
				return errors.New("transient")
			}
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, 3, calls)
	})

	t.Run("exhaustion returns last error", func(t *testing.T) {
		t.Parallel()
		calls := 0
		sentinel := errors.New("final")
		err := backoffRetry(time.Millisecond, 2, 0, func() error {
			calls++
			return sentinel
		})
		require.ErrorIs(t, err, sentinel)
		require.Equal(t, 2, calls)
	})

	t.Run("zero retries calls once", func(t *testing.T) {
		t.Parallel()
		calls := 0
		err := backoffRetry(time.Millisecond, 0, 0, func() error {
			calls++
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, 1, calls)
	})

	t.Run("jitter does not panic", func(t *testing.T) {
		t.Parallel()
		calls := 0
		_ = backoffRetry(time.Millisecond, 3, 0.5, func() error {
			calls++
			if calls < 3 {
				return errors.New("retry")
			}
			return nil
		})
		require.Equal(t, 3, calls)
	})
}

func TestCalculateNodeStatsInterval(t *testing.T) {
	t.Parallel()

	makeClient := func(readyConns int, clientsPerServer, healthCheckRate float64) *Transport {
		c := &Transport{}
		c.clientsPerServer = clientsPerServer
		c.healthCheckRate = healthCheckRate

		if readyConns > 0 {
			pool := &multiServerPool{}
			conns := make([]*Connection, readyConns)
			for i := range conns {
				conns[i] = createTestConnection("http://node:920" + string(rune('0'+i)))
			}
			pool.mu.ready = conns
			pool.mu.activeCount = readyConns
			c.mu.connectionPool = pool
		}
		return c
	}

	t.Run("clamps to minimum", func(t *testing.T) {
		t.Parallel()
		c := makeClient(1, 1.0, 100.0)
		interval := c.calculateNodeStatsInterval()
		require.Equal(t, defaultNodeStatsIntervalMin, interval)
	})

	t.Run("clamps to maximum", func(t *testing.T) {
		t.Parallel()
		c := makeClient(100, 100.0, 1.0)
		interval := c.calculateNodeStatsInterval()
		require.Equal(t, defaultNodeStatsIntervalMax, interval)
	})

	t.Run("no pool defaults to 1 node", func(t *testing.T) {
		t.Parallel()
		c := makeClient(0, 1.0, 1.0)
		interval := c.calculateNodeStatsInterval()
		require.Equal(t, defaultNodeStatsIntervalMin, interval)
	})
}

// recordingRouter records req.URL.Path as Route() saw it (before setReqURL).
// Each Route() call takes the next connection in conns, then repeats the last,
// so a prefixed seed followed by a prefix-less discovered node is expressible.
// If failAfter > 0, Route succeeds that many times then returns ErrNoConnections
// so stream() can take seed fallback on the next hop.
type recordingRouter struct {
	conns     []*Connection
	failAfter int32
	n         atomic.Int32
	mu        struct {
		sync.Mutex
		saw []string
	}
}

func (r *recordingRouter) Route(_ context.Context, req *http.Request) (NextHop, error) {
	r.mu.Lock()
	r.mu.saw = append(r.mu.saw, req.URL.Path)
	r.mu.Unlock()
	n := r.n.Add(1)
	if r.failAfter > 0 && n > r.failAfter {
		return NextHop{}, ErrNoConnections
	}
	return NextHop{Conn: r.conns[min(int(n)-1, len(r.conns)-1)]}, nil
}

func (r *recordingRouter) paths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.mu.saw...)
}

func (r *recordingRouter) OnSuccess(*Connection)                                {}
func (r *recordingRouter) OnFailure(*Connection) error                          { return nil }
func (r *recordingRouter) DiscoveryUpdate(_, _, _ []*Connection) error          { return nil }
func (r *recordingRouter) CheckDead(_ context.Context, _ HealthCheckFunc) error { return nil }
func (r *recordingRouter) RotateStandby(_ context.Context, _ int) (int, error)  { return 0, nil }

var _ Router = (*recordingRouter)(nil)

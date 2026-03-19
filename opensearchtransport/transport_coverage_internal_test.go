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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil/mockhttp"
)

func TestSetReqURL(t *testing.T) {
	t.Parallel()

	t.Run("simple URL", func(t *testing.T) {
		t.Parallel()
		c := &Client{}
		u, _ := url.Parse("https://node1:9200")
		req, _ := http.NewRequest(http.MethodGet, "/_search", nil)
		c.setReqURL(u, req)

		require.Equal(t, "https", req.URL.Scheme)
		require.Equal(t, "node1:9200", req.URL.Host)
		require.Equal(t, "/_search", req.URL.Path)
	})

	t.Run("URL with base path", func(t *testing.T) {
		t.Parallel()
		c := &Client{}
		u, _ := url.Parse("https://node1:9200/prefix")
		req, _ := http.NewRequest(http.MethodGet, "/_search", nil)
		c.setReqURL(u, req)

		require.Equal(t, "/prefix/_search", req.URL.Path)
	})

	t.Run("URL with trailing slash base path", func(t *testing.T) {
		t.Parallel()
		c := &Client{}
		u, _ := url.Parse("https://node1:9200/prefix/")
		req, _ := http.NewRequest(http.MethodGet, "/_search", nil)
		c.setReqURL(u, req)

		require.Equal(t, "/prefix/_search", req.URL.Path)
	})

	t.Run("URL with just slash path", func(t *testing.T) {
		t.Parallel()
		c := &Client{}
		u, _ := url.Parse("https://node1:9200/")
		req, _ := http.NewRequest(http.MethodGet, "/_search", nil)
		c.setReqURL(u, req)

		require.Equal(t, "/_search", req.URL.Path)
	})

	t.Run("URL with empty path", func(t *testing.T) {
		t.Parallel()
		c := &Client{}
		u, _ := url.Parse("https://node1:9200")
		req, _ := http.NewRequest(http.MethodGet, "/my-index/_doc/1", nil)
		c.setReqURL(u, req)

		require.Equal(t, "/my-index/_doc/1", req.URL.Path)
	})

	t.Run("deep path prefix", func(t *testing.T) {
		t.Parallel()
		c := &Client{}
		u, _ := url.Parse("http://proxy:8080/api/v1/opensearch")
		req, _ := http.NewRequest(http.MethodGet, "/_cat/indices", nil)
		c.setReqURL(u, req)

		require.Equal(t, "/api/v1/opensearch/_cat/indices", req.URL.Path)
	})
}

func TestSetReqAuth(t *testing.T) {
	t.Parallel()

	t.Run("auth from URL userinfo", func(t *testing.T) {
		t.Parallel()
		c := &Client{}
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
		c := &Client{username: "admin", password: "secret"}
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
		c := &Client{username: "admin", password: "secret"}
		u, _ := url.Parse("https://node1:9200")
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer token")
		c.setReqAuth(u, req)

		require.Equal(t, "Bearer token", req.Header.Get("Authorization"))
	})

	t.Run("no auth when no credentials", func(t *testing.T) {
		t.Parallel()
		c := &Client{}
		u, _ := url.Parse("https://node1:9200")
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		c.setReqAuth(u, req)

		require.Empty(t, req.Header.Get("Authorization"))
	})

	t.Run("URL userinfo takes precedence over client creds", func(t *testing.T) {
		t.Parallel()
		c := &Client{username: "client-user", password: "client-pass"}
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
		c := &Client{username: "admin"} // no password
		u, _ := url.Parse("https://node1:9200")
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		c.setReqAuth(u, req)

		_, _, ok := req.BasicAuth()
		require.False(t, ok)
	})
}

func TestSignRequest_Coverage(t *testing.T) {
	t.Parallel()

	t.Run("nil signer returns nil", func(t *testing.T) {
		t.Parallel()
		c := &Client{}
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		require.NoError(t, c.signRequest(req))
	})

	t.Run("signer adds header", func(t *testing.T) {
		t.Parallel()
		c := &Client{signer: &mockSigner{SampleKey: "X-Auth", SampleValue: "signed"}}
		req, _ := http.NewRequest(http.MethodGet, "/", nil)
		require.NoError(t, c.signRequest(req))
		require.Equal(t, "signed", req.Header.Get("X-Auth"))
	})

	t.Run("signer error propagates", func(t *testing.T) {
		t.Parallel()
		c := &Client{signer: &mockSigner{ReturnError: true}}
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
		c := &Client{}
		require.NoError(t, c.Close())
	})

	t.Run("closes with cancel func", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		c := &Client{
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
		c := &Client{transport: &closeableTransport{fn: func() { closed = true }}}
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

		c := &Client{}
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

		c := &Client{}
		c.mu.connectionPool = pool

		result := c.demoteConnectionPoolWithLock()
		require.NotNil(t, result.connection)
		require.Equal(t, "dead-node", result.connection.Name)
	})

	t.Run("multi pool with no connections", func(t *testing.T) {
		t.Parallel()
		pool := &multiServerPool{}
		c := &Client{}
		c.mu.connectionPool = pool

		result := c.demoteConnectionPoolWithLock()
		require.Nil(t, result.connection)
	})

	t.Run("already single pool returns unchanged", func(t *testing.T) {
		t.Parallel()
		u, _ := url.Parse("http://single:9200")
		conn := &Connection{URL: u, Name: "single"}
		pool := &singleServerPool{connection: conn}

		c := &Client{}
		c.mu.connectionPool = pool

		result := c.demoteConnectionPoolWithLock()
		require.Same(t, pool, result)
	})
}

func TestApplyConnectionFiltering(t *testing.T) {
	t.Parallel()

	makeConn := func(name string, roles []string) *Connection {
		return &Connection{Name: name, Roles: newRoleSet(roles)}
	}

	t.Run("excludes dedicated cluster managers", func(t *testing.T) {
		t.Parallel()
		c := &Client{includeDedicatedClusterManagers: false}

		data := makeConn("data-1", []string{"data", "ingest"})
		cm := makeConn("cm-1", []string{"cluster_manager"})
		mixed := makeConn("mixed-1", []string{"cluster_manager", "data"})

		var filteredReady, filteredDead []*Connection
		c.applyConnectionFiltering([]*Connection{data, cm, mixed}, nil, &filteredReady, &filteredDead)

		require.Len(t, filteredReady, 2)
		require.Equal(t, "data-1", filteredReady[0].Name)
		require.Equal(t, "mixed-1", filteredReady[1].Name)
	})

	t.Run("includes all when flag is true", func(t *testing.T) {
		t.Parallel()
		c := &Client{includeDedicatedClusterManagers: true}

		data := makeConn("data-1", []string{"data"})
		cm := makeConn("cm-1", []string{"cluster_manager"})

		var filteredReady, filteredDead []*Connection
		c.applyConnectionFiltering([]*Connection{data, cm}, nil, &filteredReady, &filteredDead)

		require.Len(t, filteredReady, 2)
	})

	t.Run("filters dead list too", func(t *testing.T) {
		t.Parallel()
		c := &Client{includeDedicatedClusterManagers: false}

		dataDead := makeConn("data-dead", []string{"data"})
		cmDead := makeConn("cm-dead", []string{"cluster_manager"})

		var filteredReady, filteredDead []*Connection
		c.applyConnectionFiltering(nil, []*Connection{dataDead, cmDead}, &filteredReady, &filteredDead)

		require.Len(t, filteredDead, 1)
		require.Equal(t, "data-dead", filteredDead[0].Name)
	})
}

func TestLogRoundTrip_Coverage(t *testing.T) {
	t.Parallel()

	t.Run("logs with nil response and error", func(t *testing.T) {
		t.Parallel()
		ml := &logCapture{}
		c := &Client{logger: ml}

		req, _ := http.NewRequest(http.MethodGet, "http://localhost:9200/_search", nil)
		c.logRoundTrip(req, nil, fmt.Errorf("connection refused"), time.Now(), time.Millisecond)

		require.True(t, ml.called)
		require.Error(t, ml.lastErr)
	})

	t.Run("logs with response body enabled", func(t *testing.T) {
		t.Parallel()
		ml := &logCapture{respBodyEnabled: true}
		c := &Client{logger: ml}

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
		c := &Client{logger: ml}

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

	body := bytes.NewReader([]byte(`{"query":{"match_all":{}}}`))
	req, _ := http.NewRequest(http.MethodPost, "/_search", io.NopCloser(body))
	res, err := tp.Perform(req)
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
		EnableMetrics:     true,
		NodeStatsInterval: -1, // Disable stats poller to avoid background requests through mock transport
		Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK"}, nil
		}),
	})
	require.NoError(t, err)

	req, _ := http.NewRequest(http.MethodGet, "/test", nil)
	res, err := tp.Perform(req)
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

	req, _ := http.NewRequest(http.MethodGet, "/test", nil)
	res, err := tp.Perform(req) //nolint:bodyclose // error path
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
	tp.seedFallbackDisabled = true
	tp.seedFallbackPool = nil

	req, _ := http.NewRequest(http.MethodGet, "/test", nil)
	res, err := tp.Perform(req) //nolint:bodyclose // error path
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

	req, _ := http.NewRequest(http.MethodGet, "/test", nil)
	res, err := tp.Perform(req)
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

	makeClient := func(readyConns int, clientsPerServer, healthCheckRate float64) *Client {
		c := &Client{}
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

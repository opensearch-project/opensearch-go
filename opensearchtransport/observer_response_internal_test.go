// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestResponseEventFieldPromotion(t *testing.T) {
	ev := RequestResponseEvent{
		ResponseEvent: ResponseEvent{
			Request:    RequestEvent{Method: "GET", Index: "logs"},
			StatusCode: 200,
		},
		Duration:      5 * time.Millisecond,
		ResponseBytes: 42,
	}

	require.Equal(t, 200, ev.StatusCode, "promoted StatusCode from embedded ResponseEvent")
	require.Equal(t, "logs", ev.Request.Index, "promoted Request.Index")
	require.Equal(t, "GET", ev.Request.Method)
	require.Equal(t, int64(42), ev.ResponseBytes)
	require.Equal(t, 5*time.Millisecond, ev.Duration)

	sev := StreamResponseEvent{
		ResponseEvent: ResponseEvent{
			Request:    RequestEvent{Method: "POST", Host: "http://node-1:9200"},
			StatusCode: 201,
		},
		Duration:      2 * time.Millisecond,
		ContentLength: -1,
	}
	require.Equal(t, 201, sev.StatusCode)
	require.Equal(t, "http://node-1:9200", sev.Request.Host)
	require.Equal(t, int64(-1), sev.ContentLength)
	require.Equal(t, 2*time.Millisecond, sev.Duration)
}

func TestBaseObserverResponseNoops(t *testing.T) {
	var b BaseConnectionObserver
	ctx := t.Context()
	b.OnRequestResponse(ctx, RequestResponseEvent{}) // must not panic
	b.OnStreamResponse(ctx, StreamResponseEvent{})   // must not panic

	// Compile-time proof the extended interface is still satisfied.
	var _ ConnectionObserver = (*BaseConnectionObserver)(nil)
}

func TestStreamFiresStreamResponseEvent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "5")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "hello")
	}))
	t.Cleanup(ts.Close)

	obs := newRecordingObserver()
	tp, err := New(Config{URLs: []*url.URL{mustParseURL(ts.URL)}, Observer: obs})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	require.NoError(t, err)

	res, err := tp.Stream(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	events := obs.getStreamEvents()
	require.Len(t, events, 1, "exactly one StreamResponseEvent per request")
	require.Empty(t, obs.getReqRespEvents(), "Stream must not fire RequestResponseEvent")

	got := events[0]
	require.Equal(t, http.StatusOK, got.StatusCode)
	require.Equal(t, int64(5), got.ContentLength)
	require.Greater(t, got.Duration, time.Duration(0), "TTFB should be measured")
	require.Equal(t, http.MethodGet, got.Request.Method)
	require.NoError(t, got.Err)
}

func TestRequestFiresRequestResponseEvent(t *testing.T) {
	const payload = `{"ok":true}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(ts.Close)

	obs := newRecordingObserver()
	tp, err := New(Config{URLs: []*url.URL{mustParseURL(ts.URL)}, Observer: obs})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	require.NoError(t, err)

	res, err := tp.Request(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	events := obs.getReqRespEvents()
	require.Len(t, events, 1, "exactly one RequestResponseEvent per request")
	require.Empty(t, obs.getStreamEvents(), "Request must not fire StreamResponseEvent")

	got := events[0]
	require.Equal(t, http.StatusOK, got.StatusCode)
	require.Equal(t, int64(len(payload)), got.ResponseBytes, "exact bytes read")
	require.Greater(t, got.Duration, time.Duration(0), "full-read duration should be measured")
	require.Equal(t, http.MethodGet, got.Request.Method)
	require.NoError(t, got.Err)

	// Body must remain readable after buffering.
	buf, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, payload, string(buf))
}

func TestRequestBuffersEmptyBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	obs := newRecordingObserver()
	tp, err := New(Config{URLs: []*url.URL{mustParseURL(ts.URL)}, Observer: obs})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	require.NoError(t, err)

	res, err := tp.Request(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	events := obs.getReqRespEvents()
	require.Len(t, events, 1)
	require.Equal(t, int64(0), events[0].ResponseBytes)
}

// TestRequestEventIdentityFields proves that RouteName, Index, and the escaped
// Path are captured from the caller's request and delivered on the event, and
// that non-index-scoped requests report an empty Index.
func TestRequestEventIdentityFields(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	tests := []struct {
		name          string
		method        string
		path          string
		wantRouteName string
		wantIndex     string
		wantPath      string
	}{
		{
			name:          "index-scoped doc get",
			method:        http.MethodGet,
			path:          "/logs/_doc/1",
			wantRouteName: "doc_get",
			wantIndex:     "logs",
			wantPath:      "/logs/_doc/1",
		},
		{
			name:          "index-scoped search",
			method:        http.MethodGet,
			path:          "/logs/_search",
			wantRouteName: "search",
			wantIndex:     "logs",
			wantPath:      "/logs/_search",
		},
		{
			name:          "system endpoint has empty index",
			method:        http.MethodGet,
			path:          "/_cluster/health",
			wantRouteName: "cluster_health",
			wantIndex:     "",
			wantPath:      "/_cluster/health",
		},
		{
			// EscapedPath preserves %2F, but classification and index
			// extraction run on the decoded req.URL.Path ("/logs/_doc/a/b"),
			// which has an extra segment and so does not match doc_get.
			name:          "escaped path preserves percent-encoding",
			method:        http.MethodGet,
			path:          "/logs/_doc/a%2Fb",
			wantRouteName: "other",
			wantIndex:     "logs",
			wantPath:      "/logs/_doc/a%2Fb",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			obs := newRecordingObserver()
			tp, err := New(Config{URLs: []*url.URL{mustParseURL(ts.URL)}, Observer: obs})
			require.NoError(t, err)

			req, err := http.NewRequest(tc.method, ts.URL+tc.path, nil)
			require.NoError(t, err)

			res, err := tp.Request(req)
			require.NoError(t, err)
			t.Cleanup(func() { _ = res.Body.Close() })

			events := obs.getReqRespEvents()
			require.Len(t, events, 1)
			got := events[0].Request
			require.Equal(t, tc.wantRouteName, got.RouteName)
			require.Equal(t, tc.wantIndex, got.Index)
			require.Equal(t, tc.wantPath, got.Path)
		})
	}
}

// TestRequestEventIdentityIgnoresBasePath proves RouteName/Index/Path are
// derived from the caller's path, not the backend URL after a connection base
// path has been prepended.
func TestRequestEventIdentityIgnoresBasePath(t *testing.T) {
	var gotServerPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotServerPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	obs := newRecordingObserver()
	// Connection URL carries a base path prefix.
	tp, err := New(Config{URLs: []*url.URL{mustParseURL(ts.URL + "/os")}, Observer: obs})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/logs/_doc/1", nil)
	require.NoError(t, err)

	res, err := tp.Request(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	require.Equal(t, "/os/logs/_doc/1", gotServerPath, "backend receives the base-path-prefixed path")

	events := obs.getReqRespEvents()
	require.Len(t, events, 1)
	got := events[0].Request
	require.Equal(t, "doc_get", got.RouteName, "route derived from pre-rewrite path")
	require.Equal(t, "logs", got.Index, "index derived from pre-rewrite path")
	require.Equal(t, "/logs/_doc/1", got.Path, "escaped path is the caller input, no base prefix")
}

// TestOperationClassifierResolution proves the transport uses a caller-supplied
// classifier when set and the shared default otherwise.
func TestOperationClassifierResolution(t *testing.T) {
	custom := NewOperationClassifier()

	withCustom := &Transport{classifier: custom}
	require.Same(t, custom, withCustom.operationClassifier(),
		"a configured classifier is used verbatim")

	defaulted := &Transport{}
	require.Same(t, defaultOperationClassifier(), defaulted.operationClassifier(),
		"an unset classifier falls back to the shared global")
}

// TestConfigOperationClassifierWiring proves Config.OperationClassifier reaches
// the transport field and is honored end-to-end.
func TestConfigOperationClassifierWiring(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	custom := NewOperationClassifier()
	obs := newRecordingObserver()
	tp, err := New(Config{
		URLs:                []*url.URL{mustParseURL(ts.URL)},
		Observer:            obs,
		OperationClassifier: custom,
	})
	require.NoError(t, err)
	require.Same(t, custom, tp.classifier, "Config.OperationClassifier is stored on the transport")

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/logs/_search", nil)
	require.NoError(t, err)
	res, err := tp.Request(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	events := obs.getReqRespEvents()
	require.Len(t, events, 1)
	require.Equal(t, "search", events[0].Request.RouteName)
}

// traceKey is a context key used by tracingObserver to prove the context
// returned by OnRequestStart flows into the attempt and response hooks.
type traceKey struct{}

// tracingObserver records the tracing-hook call sequence and verifies that the
// context threaded from OnRequestStart reaches every later hook. It embeds
// BaseConnectionObserver, so only the tracing hooks are overridden.
type tracingObserver struct {
	BaseConnectionObserver

	mu           sync.Mutex
	calls        []string // ordered hook names
	startEvent   RequestEvent
	attemptCtxOK []bool // per-attempt: did the request-scoped value reach OnAttemptStart?
	respCtxOK    bool   // did it reach the response hook?
	attempts     int
}

func (o *tracingObserver) OnRequestStart(ctx context.Context, event RequestEvent) context.Context {
	o.mu.Lock()
	o.calls = append(o.calls, "start")
	o.startEvent = event
	o.mu.Unlock()
	return context.WithValue(ctx, traceKey{}, "span")
}

func (o *tracingObserver) OnAttemptStart(ctx context.Context, attempt int) context.Context {
	o.mu.Lock()
	o.calls = append(o.calls, "attempt_start")
	o.attemptCtxOK = append(o.attemptCtxOK, ctx.Value(traceKey{}) == "span")
	o.mu.Unlock()
	return ctx
}

func (o *tracingObserver) OnAttemptEnd(_ context.Context, attempt int, _ int, _ error) {
	o.mu.Lock()
	o.calls = append(o.calls, "attempt_end")
	o.attempts++
	o.mu.Unlock()
}

func (o *tracingObserver) OnRequestResponse(ctx context.Context, _ RequestResponseEvent) {
	o.mu.Lock()
	o.calls = append(o.calls, "response")
	o.respCtxOK = ctx.Value(traceKey{}) == "span"
	o.mu.Unlock()
}

// TestTracingHooksLifecycle proves the request-scoped context returned by
// OnRequestStart flows into OnAttemptStart and OnRequestResponse, and that the
// hooks fire in order (start, then per-attempt start/end, then response).
func TestTracingHooksLifecycle(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	obs := &tracingObserver{}
	tp, err := New(Config{URLs: []*url.URL{mustParseURL(ts.URL)}, Observer: obs})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/logs/_search", nil)
	require.NoError(t, err)
	res, err := tp.Request(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	obs.mu.Lock()
	defer obs.mu.Unlock()
	require.Equal(t, []string{"start", "attempt_start", "attempt_end", "response"}, obs.calls,
		"hooks fire in lifecycle order")
	require.Equal(t, "search", obs.startEvent.RouteName, "OnRequestStart sees the classified route")
	require.Equal(t, []bool{true}, obs.attemptCtxOK, "request-scoped context reaches OnAttemptStart")
	require.True(t, obs.respCtxOK, "request-scoped context reaches OnRequestResponse")
	require.Equal(t, 1, obs.attempts)
}

// TestNewRequestEventZeroAlloc guards that building the RequestEvent snapshot
// fired at observers allocates nothing: all fields are scalars or strings copied
// from the pre-captured streamResult (Host from the connection's cached
// hostPort), so the value stays on the stack.
func TestNewRequestEventZeroAlloc(t *testing.T) {
	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Scheme: "http", Host: "node-1:9200", Path: "/idx/_search"},
	}
	sr := streamResult{
		escapedPath: "/idx/_search",
		routeName:   "search",
		index:       "idx",
		poolName:    "search",
		hostPort:    "http://node-1:9200",
	}
	allocs := testing.AllocsPerRun(100, func() {
		ev := newRequestEvent(req, sr)
		_ = ev
	})
	require.Zero(t, allocs, "newRequestEvent must not allocate")
}

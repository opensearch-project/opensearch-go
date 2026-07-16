// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestBaseObserverResponseNoops(_ *testing.T) {
	var b BaseConnectionObserver
	b.OnRequestResponse(RequestResponseEvent{}) // must not panic
	b.OnStreamResponse(StreamResponseEvent{})   // must not panic

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

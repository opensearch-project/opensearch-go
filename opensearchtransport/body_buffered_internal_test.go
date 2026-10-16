// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	bufbody "github.com/opensearch-project/opensearch-go/v5/internal/body"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

// countingBody is a bufbody.BufferedReader that counts how it is read, so a test
// can tell a body that was copied from one that was replayed in place.
type countingBody struct {
	data  []byte
	off   int
	reads atomic.Int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	b.reads.Add(1)
	if b.off >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.off:])
	b.off += n
	return n, nil
}

func (b *countingBody) Close() error         { return nil }
func (b *countingBody) Len() int             { return len(b.data) - b.off }
func (b *countingBody) NewReader() io.Reader { return bytes.NewReader(b.data) }

func TestRequestBodyHandling(t *testing.T) {
	t.Parallel()

	const payload = `{"a":1}`

	tests := []struct {
		name string
		body func() io.ReadCloser
		// wantBufferedInPlace is set when the transport must replay the body from
		// the memory it is in, without reading it to copy it.
		wantBufferedInPlace bool
	}{
		{
			name:                "a body held in memory is replayed in place",
			body:                func() io.ReadCloser { return &countingBody{data: []byte(payload)} },
			wantBufferedInPlace: true,
		},
		{
			name: "a streamed body is buffered once so it can be replayed",
			body: func() io.ReadCloser { return io.NopCloser(io.MultiReader(strings.NewReader(payload))) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body := tt.body()

			var sawLength int64
			var sawGetBody, sawCallersBody bool
			var sawBody string
			tp, err := New(Config{
				URLs:              []*url.URL{{}},
				NodeStatsInterval: -1,
				Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
					sawCallersBody = req.Body == body
					got, readErr := io.ReadAll(req.Body)
					if readErr != nil {
						return nil, readErr
					}
					sawLength, sawGetBody, sawBody = req.ContentLength, req.GetBody != nil, string(got)
					return &http.Response{StatusCode: http.StatusOK, Status: "MOCK", Body: http.NoBody}, nil
				}),
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = tp.Close() })

			req, err := http.NewRequest(http.MethodPost, "/abc", body)
			require.NoError(t, err)
			req.ContentLength = 0
			req.GetBody = nil

			res, err := tp.Stream(req)
			require.NoError(t, err)
			_ = res.Body.Close()

			require.Equal(t, payload, sawBody)
			require.True(t, sawGetBody, "the request must be replayable for a retry")

			if tt.wantBufferedInPlace {
				require.EqualValues(t, len(payload), sawLength, "a body that knows its length states it")
				// A copy would put a snapshot reader on the wire in its place.
				require.True(t, sawCallersBody, "the wire must see the caller's own body")
			} else {
				require.False(t, sawCallersBody, "a streamed body is replaced by its buffered copy")
			}
		})
	}
}

func TestRequestReturnsABufferedResponse(t *testing.T) {
	t.Parallel()

	const payload = `{"ok":true}`

	tp, err := New(Config{
		URLs:              []*url.URL{{}},
		NodeStatsInterval: -1,
		Transport: mockhttp.NewRoundTripFunc(t, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Status: "MOCK", Body: io.NopCloser(strings.NewReader(payload))}, nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })

	req, err := http.NewRequest(http.MethodGet, "/abc", nil)
	require.NoError(t, err)

	res, err := tp.Request(req)
	require.NoError(t, err)

	contiguous, ok := res.Body.(bufbody.ContiguousReader)
	require.True(t, ok, "%T must expose the bytes it holds", res.Body)
	require.Equal(t, payload, string(contiguous.Bytes()))
	require.NoError(t, res.Body.Close())
}

// TestRequestRetryReplaysTheWholeBody: the first attempt reads the body to its end,
// and a retry has to send it all again, not what is left of it.
func TestRequestRetryReplaysTheWholeBody(t *testing.T) {
	t.Parallel()

	const payload = `{"a":1,"b":[1,2,3]}`

	var (
		mu     sync.Mutex
		bodies []string
	)
	tp, err := New(Config{
		URLs:              []*url.URL{{}},
		NodeStatsInterval: -1,
		Transport: mockhttp.NewRoundTripFunc(t, func(req *http.Request) (*http.Response, error) {
			got, readErr := io.ReadAll(req.Body)
			if readErr != nil {
				return nil, readErr
			}

			mu.Lock()
			defer mu.Unlock()
			bodies = append(bodies, string(got))

			status := http.StatusOK
			if len(bodies) == 1 {
				status = http.StatusServiceUnavailable
			}

			return &http.Response{StatusCode: status, Body: http.NoBody}, nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })

	req, err := http.NewRequest(http.MethodPost, "/_bulk", &countingBody{data: []byte(payload)})
	require.NoError(t, err)
	req.ContentLength, req.GetBody = 0, nil

	res, err := tp.Stream(req)
	require.NoError(t, err)
	_ = res.Body.Close()

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{payload, payload}, bodies, "the retry must carry the whole body")
}

// closeTrackingBody is a countingBody that counts its Close calls.
type closeTrackingBody struct {
	countingBody
	closed atomic.Int64
}

//nolint:unparam // Keeps the io.Closer signature.
func (b *closeTrackingBody) Close() error {
	b.closed.Add(1)
	return nil
}

// TestStreamClosesBodyOfRequestItFailsToSend: a request that fails before any
// round trip, here because it cannot be signed, still has its body closed, so a
// body that lends memory is released.
func TestStreamClosesBodyOfRequestItFailsToSend(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("http://example.com")
	require.NoError(t, err)

	var roundTrips atomic.Int64
	tp, err := New(Config{
		URLs:   []*url.URL{u},
		Signer: &mockSigner{ReturnError: true},
		Transport: mockhttp.NewRoundTripFunc(t, func(*http.Request) (*http.Response, error) {
			roundTrips.Add(1)
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })

	body := &closeTrackingBody{countingBody: countingBody{data: []byte(`{"a":1}`)}}
	req, err := http.NewRequest(http.MethodPost, "/_bulk", body)
	require.NoError(t, err)

	//nolint:bodyclose // the request fails before a response exists
	_, err = tp.Stream(req)
	require.ErrorContains(t, err, "failed to sign request")
	require.Zero(t, roundTrips.Load(), "nothing was sent")
	require.Equal(t, int64(1), body.closed.Load(), "the body of the unsent request is closed")
}

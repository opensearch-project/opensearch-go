// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"cmp"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

func TestRequestRecordsLatency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string // empty reads as GET
		path       string // empty reads as /index/_doc/1
		streamed   bool
		status     int
		roundTrip  error
		wantSample bool
	}{
		{name: "buffered 200", status: http.StatusOK, wantSample: true},
		{name: "buffered 404", status: http.StatusNotFound, wantSample: true},
		{name: "streamed 200 is time to first byte only", streamed: true, status: http.StatusOK},
		{name: "transport error", roundTrip: errors.New("connection reset")},
		{name: "500", status: http.StatusInternalServerError},
		{name: "503", status: http.StatusServiceUnavailable},
		{name: "429", status: http.StatusTooManyRequests},
		{name: "search 200", method: http.MethodPost, path: "/index/_search", status: http.StatusOK, wantSample: true},
		{name: "scroll page", method: http.MethodPost, path: "/_search/scroll", status: http.StatusOK},
		{name: "scroll delete", method: http.MethodDelete, path: "/_search/scroll", status: http.StatusOK},
		{name: "point-in-time create", method: http.MethodPost, path: "/index/_search/point_in_time", status: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tr, _, conns := newRouterPathTransport(t, &callerHealthObserver{recordingObserver: newRecordingObserver()})
			// The test router sends every request to the get pool.
			method, path := cmp.Or(tt.method, http.MethodGet), cmp.Or(tt.path, "/index/_doc/1")
			tr.disableRetry = true
			tr.transport = mockhttp.NewRoundTripFunc(t, func(*http.Request) (*http.Response, error) {
				if tt.roundTrip != nil {
					return nil, tt.roundTrip
				}
				return &http.Response{StatusCode: tt.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
			})

			req, err := http.NewRequestWithContext(t.Context(), method, path, nil)
			require.NoError(t, err)

			if tt.streamed {
				res, err := tr.Stream(req)
				require.NoError(t, err)
				require.NoError(t, res.Body.Close())
			} else {
				res, err := tr.Request(req)
				if res != nil {
					require.NoError(t, res.Body.Close())
				}
				_ = err // 5xx, 429 and transport errors are the cases under test
			}

			var sampled int
			for _, conn := range conns {
				if _, ok := conn.pools.get(poolGet).latency.load(); ok {
					sampled++
				}
			}
			require.Equal(t, boolToInt(tt.wantSample), sampled)
		})
	}
}

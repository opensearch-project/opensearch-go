// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// TestRestStatusJSON covers the generated int-backed enum's JSON behavior:
// known values round-trip by wire name, and an unknown value sets the Unknown
// sentinel and returns a recoverable *UnknownRestStatusError.
func TestRestStatusJSON(t *testing.T) {
	t.Run("unmarshal known values", func(t *testing.T) {
		tests := []struct {
			name string
			wire string
			want opensearchapi.RestStatus
		}{
			{name: "ok", wire: `"OK"`, want: opensearchapi.RestStatusOk},
			{name: "not found", wire: `"NOT_FOUND"`, want: opensearchapi.RestStatusNotFound},
			{name: "created", wire: `"CREATED"`, want: opensearchapi.RestStatusCreated},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var s opensearchapi.RestStatus
				require.NoError(t, json.Unmarshal([]byte(tt.wire), &s))
				require.Equal(t, tt.want, s)
			})
		}
	})

	t.Run("marshal", func(t *testing.T) {
		tests := []struct {
			name    string
			in      opensearchapi.RestStatus
			want    string // expected JSON wire form (when wantErr is false)
			wantErr bool   // Unknown/out-of-range values are not encodable
		}{
			{name: "ok", in: opensearchapi.RestStatusOk, want: `"OK"`},
			{name: "created", in: opensearchapi.RestStatusCreated, want: `"CREATED"`},
			{name: "acronym", in: opensearchapi.RestStatusHTTPVersionNotSupported, want: `"HTTP_VERSION_NOT_SUPPORTED"`},
			// The zero value is the Unknown sentinel; it has no wire name and
			// MarshalJSON errors rather than emit a bogus status. Reachable only
			// by marshaling a hand-constructed value (response fields are
			// pointers with omitempty, so a nil Unknown is omitted).
			{name: "unknown sentinel errors", in: opensearchapi.RestStatusUnknown, wantErr: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				b, err := json.Marshal(tt.in)
				if tt.wantErr {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				require.JSONEq(t, tt.want, string(b))
				// String() reports the same wire name.
				require.Equal(t, tt.want, `"`+tt.in.String()+`"`)
			})
		}
	})

	t.Run("round-trip", func(t *testing.T) {
		for _, status := range []opensearchapi.RestStatus{
			opensearchapi.RestStatusOk,
			opensearchapi.RestStatusCreated,
			opensearchapi.RestStatusHTTPVersionNotSupported,
		} {
			b, err := json.Marshal(status)
			require.NoError(t, err)
			var got opensearchapi.RestStatus
			require.NoError(t, json.Unmarshal(b, &got))
			require.Equal(t, status, got)
		}
	})

	t.Run("unknown value sets sentinel and returns recoverable error", func(t *testing.T) {
		tests := []struct {
			name   string
			wire   string
			target func() any // fresh decode target per case
			raw    string
		}{
			{
				name:   "bare string",
				wire:   `"I_AM_A_TEAPOT"`,
				target: func() any { return new(opensearchapi.RestStatus) },
				raw:    "I_AM_A_TEAPOT",
			},
			{
				name: "within struct",
				wire: `{"status":"FUTURE_STATUS"}`,
				target: func() any {
					return &struct {
						Status *opensearchapi.RestStatus `json:"status"`
					}{}
				},
				raw: "FUTURE_STATUS",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				err := json.Unmarshal([]byte(tt.wire), tt.target())

				var unknownErr *opensearchapi.UnknownRestStatusError
				require.ErrorAs(t, err, &unknownErr)
				require.Equal(t, tt.raw, unknownErr.Value)
			})
		}
	})
}

// staticRequest is a minimal opensearch.Request that targets a fixed path, used
// to drive the real client decode path against an httptest server.
type staticRequest struct {
	url string
}

func (r staticRequest) GetRequest(method string) (*http.Request, error) {
	return http.NewRequest(method, r.url, http.NoBody)
}

// TestRestStatusClientDecode drives the real client.Do decode path (the same
// json.Unmarshal + error wrap used for every API response) against a canned
// body, proving the end-to-end contract Sean asked for: on an unknown status
// the caller receives BOTH the *Response (with the raw body preserved for
// inspection) AND a recoverable *UnknownRestStatusError carrying the raw value.
//
// Note: whether sibling fields are populated on error is an encoding/json
// detail (it decodes in document order and aborts at the failing field), so
// this test asserts only the stable contract, not partial-decode state.
func TestRestStatusClientDecode(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantErr    bool
		wantRaw    string                   // unknown wire value carried on the error (when wantErr)
		wantStatus opensearchapi.RestStatus // decoded value (when !wantErr)
	}{
		{
			name:    "unknown status returns response and recoverable error",
			body:    `{"status":"FUTURE_STATUS","message":"hello"}`,
			wantErr: true,
			wantRaw: "FUTURE_STATUS",
		},
		{
			name:       "known status decodes cleanly with no error",
			body:       `{"status":"OK"}`,
			wantErr:    false,
			wantStatus: opensearchapi.RestStatusOk,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			t.Cleanup(ts.Close)

			client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{ts.URL}})
			require.NoError(t, err)

			var body struct {
				Status *opensearchapi.RestStatus `json:"status"`
			}
			resp, err := opensearch.Execute(t.Context(), client, http.MethodGet, staticRequest{url: ts.URL}, &body)

			if !tt.wantErr {
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.NotNil(t, body.Status)
				require.Equal(t, tt.wantStatus, *body.Status)
				return
			}

			// Both the response and the error are returned (Sean's contract).
			require.Error(t, err)
			require.NotNil(t, resp, "response must be returned alongside the decode error")
			// The raw body is preserved on the response for inspection.
			require.Contains(t, resp.String(), tt.wantRaw)
			// The unknown value is recoverable from the error.
			var unknownErr *opensearchapi.UnknownRestStatusError
			require.ErrorAs(t, err, &unknownErr)
			require.Equal(t, tt.wantRaw, unknownErr.Value)
		})
	}
}

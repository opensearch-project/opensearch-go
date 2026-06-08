// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearch_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
)

func TestParseError_TypedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		body       string
		check      func(t *testing.T, err error)
	}{
		{
			name:       "struct error with root cause",
			statusCode: http.StatusBadRequest,
			body: `{
				"error":{
					"root_cause":[{
						"type":"resource_already_exists_exception",
						"reason":"index [test/HU2mN_RMRXGcS38j3yV-VQ] already exists",
						"index":"test",
						"index_uuid":"HU2mN_RMRXGcS38j3yV-VQ"
					}],
					"type":"resource_already_exists_exception",
					"reason":"index [test/HU2mN_RMRXGcS38j3yV-VQ] already exists",
					"index":"test",
					"index_uuid":"HU2mN_RMRXGcS38j3yV-VQ"
				},
				"status":400
			}`,
			check: func(t *testing.T, err error) {
				t.Helper()
				var e *opensearch.StructError
				require.ErrorAs(t, err, &e)
				require.Equal(t, http.StatusBadRequest, e.Status)
				require.Equal(t, "resource_already_exists_exception", e.Err.Type)
				require.Equal(t, "index [test/HU2mN_RMRXGcS38j3yV-VQ] already exists", e.Err.Reason)
				require.Equal(t, "test", e.Err.Index)
				require.Equal(t, "HU2mN_RMRXGcS38j3yV-VQ", e.Err.IndexUUID)
				require.NotNil(t, e.Err.RootCause)
				require.Equal(t, "resource_already_exists_exception", e.Err.RootCause[0].Type)
				require.Equal(t, "index [test/HU2mN_RMRXGcS38j3yV-VQ] already exists", e.Err.RootCause[0].Reason)
				require.Equal(t, "test", e.Err.RootCause[0].Index)
				require.Equal(t, "HU2mN_RMRXGcS38j3yV-VQ", e.Err.RootCause[0].IndexUUID)
			},
		},
		{
			name:       "struct error with caused_by chain",
			statusCode: http.StatusBadRequest,
			body: `{
				"error":{
					"root_cause":[{
						"type":"illegal_argument_exception",
						"reason":"composable template [posts] template after composition is invalid"
					}],
					"type":"illegal_argument_exception",
					"reason":"composable template [posts] template after composition is invalid",
					"caused_by":{
						"type":"illegal_argument_exception",
						"reason":"Custom analyzer [mm_analyzer] failed to find filter under name [test_filter]",
						"caused_by":{
							"type":"illegal_argument_exception",
							"reason":"test caused by"
						}
					}
				},
				"status":400
			}`,
			check: func(t *testing.T, err error) {
				t.Helper()
				var e *opensearch.StructError
				require.ErrorAs(t, err, &e)
				require.Equal(t, http.StatusBadRequest, e.Status)
				require.Equal(t, "illegal_argument_exception", e.Err.Type)
				require.Equal(t, "composable template [posts] template after composition is invalid", e.Err.Reason)
				require.NotNil(t, e.Err.RootCause)
				require.Equal(t, "illegal_argument_exception", e.Err.RootCause[0].Type)
				require.NotNil(t, e.Err.CausedBy)
				require.Equal(t, "illegal_argument_exception", e.Err.CausedBy.Type)
				require.Equal(t, "Custom analyzer [mm_analyzer] failed to find filter under name [test_filter]", e.Err.CausedBy.Reason)
				require.NotNil(t, e.Err.CausedBy.CausedBy)
				require.Equal(t, "test caused by", e.Err.CausedBy.CausedBy.Reason)
				require.Nil(t, e.Err.CausedBy.CausedBy.CausedBy)
			},
		},
		{
			name:       "string error",
			statusCode: http.StatusMethodNotAllowed,
			body:       `{"error":"Incorrect HTTP method for uri [/_doc] and method [POST], allowed: [HEAD, DELETE, PUT, GET]","status":405}`,
			check: func(t *testing.T, err error) {
				t.Helper()
				var e *opensearch.StringError
				require.ErrorAs(t, err, &e)
				require.Equal(t, http.StatusMethodNotAllowed, e.Status)
				require.Contains(t, e.Err, "Incorrect HTTP method for uri")
			},
		},
		{
			name:       "string error from unknown JSON shape",
			statusCode: http.StatusNotFound,
			body:       `{"_index":"index","_id":"2","matched":false}`,
			check: func(t *testing.T, err error) {
				t.Helper()
				var e *opensearch.StringError
				require.ErrorAs(t, err, &e)
				require.Equal(t, http.StatusNotFound, e.Status)
				require.Contains(t, e.Err, `{"_index":"index","_id":"2","matched":false}`)
			},
		},
		{
			name:       "error with string error field and no status",
			statusCode: http.StatusBadRequest,
			body:       `{"error":"no handler found for uri [/_plugins/_security/xxx] and method [GET]"}`,
			check: func(t *testing.T, err error) {
				t.Helper()
				var e *opensearch.Error
				require.ErrorAs(t, err, &e)
				require.Contains(t, e.Err, "no handler found for uri [/_plugins/_security/xxx] and method [GET]")
			},
		},
		{
			name:       "reason error",
			statusCode: http.StatusBadRequest,
			body:       `{"status":"error","reason":"Invalid configuration","invalid_keys":{"keys":"dynamic"}}`,
			check: func(t *testing.T, err error) {
				t.Helper()
				var e *opensearch.ReasonError
				require.ErrorAs(t, err, &e)
				require.Equal(t, "error", e.Status)
				require.Contains(t, e.Reason, "Invalid configuration")
			},
		},
		{
			name:       "message error",
			statusCode: http.StatusBadRequest,
			body:       `{"status":"BAD_REQUEST","message":"Wrong request body"}`,
			check: func(t *testing.T, err error) {
				t.Helper()
				var e *opensearch.MessageError
				require.ErrorAs(t, err, &e)
				require.Equal(t, "BAD_REQUEST", e.Status)
				require.Contains(t, e.Message, "Wrong request body")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := opensearch.NewResponse(tt.statusCode, io.NopCloser(strings.NewReader(tt.body)), nil)
			require.True(t, resp.IsError())
			err := opensearch.ParseError(resp)
			tt.check(t, err)
			_ = fmt.Sprintf("%s", err)
		})
	}
}

func TestParseError_SentinelErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		body       io.ReadCloser
		wantErrors []error
	}{
		{
			name:       "error field is object not string",
			statusCode: http.StatusForbidden,
			body:       io.NopCloser(strings.NewReader(`{"error":{"test": "test"},"status":403}`)),
			wantErrors: []error{opensearch.ErrJSONUnmarshalBody, opensearch.ErrUnknownOpensearchError},
		},
		{
			name:       "io read error",
			statusCode: http.StatusBadRequest,
			body:       io.NopCloser(iotest.ErrReader(errors.New("io reader test"))),
			wantErrors: []error{opensearch.ErrReadBody},
		},
		{
			name:       "empty body",
			statusCode: http.StatusBadRequest,
			body:       nil,
			wantErrors: []error{opensearch.ErrUnexpectedEmptyBody},
		},
		{
			name:       "unmarshal error on non-object JSON",
			statusCode: http.StatusNotFound,
			body:       io.NopCloser(strings.NewReader(`"test"`)),
			wantErrors: []error{opensearch.ErrJSONUnmarshalBody},
		},
		{
			name:       "unauthorized plain text",
			statusCode: http.StatusUnauthorized,
			body:       io.NopCloser(strings.NewReader(http.StatusText(http.StatusUnauthorized))),
			wantErrors: []error{opensearch.ErrJSONUnmarshalBody},
		},
		{
			name:       "too many requests plain text",
			statusCode: http.StatusTooManyRequests,
			body:       io.NopCloser(strings.NewReader("429 Too Many Requests /testindex/_bulk")),
			wantErrors: []error{opensearch.ErrJSONUnmarshalBody},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := opensearch.NewResponse(tt.statusCode, tt.body, nil)
			err := opensearch.ParseError(resp)
			for _, want := range tt.wantErrors {
				require.ErrorIs(t, err, want)
			}
		})
	}
}

func TestStructError_Unmarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		body  string
		check func(t *testing.T, err error)
	}{
		{
			name: "status as string instead of int",
			body: `{"status": "400"}`,
			check: func(t *testing.T, err error) {
				t.Helper()
				var jsonError *json.UnmarshalTypeError
				require.ErrorAs(t, err, &jsonError)
			},
		},
		{
			name: "error as number triggers StringError",
			body: `{"error": 0, "status": 500}`,
			check: func(t *testing.T, err error) {
				t.Helper()
				var errStr *opensearch.StringError
				require.ErrorAs(t, err, &errStr)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var errStruct *opensearch.StructError
			err := json.Unmarshal([]byte(tt.body), &errStruct)
			require.Error(t, err)
			tt.check(t, err)
		})
	}
}

// TestParseError_BodyState exercises the Body invariant on both arms: on
// success ParseError must restore Body with the original bytes, and on the
// io.ReadAll error path it must still leave resp.Body in a usable state -- a
// NopCloser the caller can Read or Close without observing a closed reader.
func TestParseError_BodyState(t *testing.T) {
	t.Parallel()

	const okBody = `{"error":{"type":"resource_already_exists_exception",` +
		`"reason":"index [test/HU2mN_RMRXGcS38j3yV-VQ] already exists"},"status":400}`

	syntheticReadErr := errors.New("synthetic read failure")

	tests := []struct {
		name       string
		statusCode int
		body       io.ReadCloser
		// expectedBody, when non-empty, is JSON-compared against what the
		// restored Body produces after ParseError returns.
		expectedBody string
		// errIs lists sentinel errors the returned err must wrap.
		errIs []error
	}{
		{
			name:         "success path restores body bytes",
			statusCode:   http.StatusBadRequest,
			body:         io.NopCloser(strings.NewReader(okBody)),
			expectedBody: okBody,
		},
		{
			name:       "read error path leaves body readable",
			statusCode: http.StatusInternalServerError,
			body:       io.NopCloser(iotest.ErrReader(syntheticReadErr)),
			errIs:      []error{opensearch.ErrReadBody, syntheticReadErr},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := opensearch.NewResponse(tt.statusCode, tt.body, nil)
			err := opensearch.ParseError(resp)
			require.Error(t, err)

			for _, target := range tt.errIs {
				require.ErrorIs(t, err, target)
			}

			require.NotNil(t, resp.Body, "Body must be restored, not left nil")
			body, readErr := io.ReadAll(resp.Body)
			require.NoError(t, readErr, "restored Body must be readable without error")
			require.NoError(t, resp.Body.Close())

			if tt.expectedBody != "" {
				require.NotEmpty(t, body)
				require.JSONEq(t, tt.expectedBody, string(body))
			}
		})
	}
}

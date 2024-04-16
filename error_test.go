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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
)

func TestError(t *testing.T) {
	t.Run("StructError", func(t *testing.T) {
		t.Run("Ok", func(t *testing.T) {
			resp := &opensearch.Response{
				StatusCode: http.StatusBadRequest,
				Body: io.NopCloser(
					strings.NewReader(`{
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
					}`),
				),
			}
			assert.True(t, resp.IsError())
			err := opensearch.ParseError(resp)
			var testError *opensearch.StructError
			require.True(t, errors.As(err, &testError))
			assert.Equal(t, http.StatusBadRequest, testError.Status)
			assert.Equal(t, "resource_already_exists_exception", testError.Err.Type)
			assert.Equal(t, "index [test/HU2mN_RMRXGcS38j3yV-VQ] already exists", testError.Err.Reason)
			assert.Equal(t, "test", testError.Err.Index)
			assert.Equal(t, "HU2mN_RMRXGcS38j3yV-VQ", testError.Err.IndexUUID)
			assert.NotNil(t, testError.Err.RootCause)
			assert.Equal(t, "resource_already_exists_exception", testError.Err.RootCause[0].Type)
			assert.Equal(t, "index [test/HU2mN_RMRXGcS38j3yV-VQ] already exists", testError.Err.RootCause[0].Reason)
			assert.Equal(t, "test", testError.Err.RootCause[0].Index)
			assert.Equal(t, "HU2mN_RMRXGcS38j3yV-VQ", testError.Err.RootCause[0].IndexUUID)
			_ = fmt.Sprintf("%s", err)
		})

		t.Run("Unmarshal errors", func(t *testing.T) {
			t.Run("dummy", func(t *testing.T) {
				reader := io.NopCloser(
					strings.NewReader(`{
						"status": "400"
					}`),
				)
				body, err := io.ReadAll(reader)
				require.Nil(t, err)

				var errStruct *opensearch.StructError
				err = json.Unmarshal(body, &errStruct)
				assert.NotNil(t, err)

				var jsonError *json.UnmarshalTypeError
				assert.True(t, errors.As(err, &jsonError))
			})
			t.Run("string", func(t *testing.T) {
				reader := io.NopCloser(
					strings.NewReader(`{
						"error": 0,
						"status": 500
					}`),
				)
				body, err := io.ReadAll(reader)
				require.Nil(t, err)

				var errStruct *opensearch.StructError
				err = json.Unmarshal(body, &errStruct)
				assert.NotNil(t, err)

				var errStr *opensearch.StringError
				require.True(t, errors.As(err, &errStr))
			})
		})
	})

	t.Run("StringError", func(t *testing.T) {
		resp := &opensearch.Response{
			StatusCode: http.StatusMethodNotAllowed,
			Body: io.NopCloser(
				strings.NewReader(`{
						"error": "Incorrect HTTP method for uri [/_doc] and method [POST], allowed: [HEAD, DELETE, PUT, GET]",
						"status":405
					}`),
			),
		}
		assert.True(t, resp.IsError())
		err := opensearch.ParseError(resp)
		var testError *opensearch.StringError
		require.True(t, errors.As(err, &testError))
		assert.Equal(t, http.StatusMethodNotAllowed, testError.Status)
		assert.Contains(t, testError.Err, "Incorrect HTTP method for uri")
		_ = fmt.Sprintf("%s", testError)
	})

	t.Run("Error", func(t *testing.T) {
		resp := &opensearch.Response{
			StatusCode: http.StatusBadRequest,
			Body: io.NopCloser(
				strings.NewReader(`{
					  "error": "no handler found for uri [/_plugins/_security/xxx] and method [GET]"
					}`),
			),
		}
		assert.True(t, resp.IsError())
		err := opensearch.ParseError(resp)
		var testError *opensearch.Error
		require.True(t, errors.As(err, &testError))
		assert.Contains(t, testError.Err, "no handler found for uri [/_plugins/_security/xxx] and method [GET]")
		_ = fmt.Sprintf("%s", testError)
	})

	t.Run("ReasonError", func(t *testing.T) {
		resp := &opensearch.Response{
			StatusCode: http.StatusBadRequest,
			Body: io.NopCloser(
				strings.NewReader(`{
					  "status": "error",
					  "reason": "Invalid configuration",
					  "invalid_keys": {
					    "keys": "dynamic"
					  }
					}`),
			),
		}
		assert.True(t, resp.IsError())
		err := opensearch.ParseError(resp)
		var testError *opensearch.ReasonError
		require.True(t, errors.As(err, &testError))
		assert.Equal(t, "error", testError.Status)
		assert.Contains(t, testError.Reason, "Invalid configuration")
		_ = fmt.Sprintf("%s", testError)
	})

	t.Run("MessageError", func(t *testing.T) {
		resp := &opensearch.Response{
			StatusCode: http.StatusBadRequest,
			Body: io.NopCloser(
				strings.NewReader(`{"status":"BAD_REQUEST","message":"Wrong request body"}`),
			),
		}
		assert.True(t, resp.IsError())
		err := opensearch.ParseError(resp)
		var testError *opensearch.MessageError
		require.True(t, errors.As(err, &testError))
		assert.Equal(t, "BAD_REQUEST", testError.Status)
		assert.Contains(t, testError.Message, "Wrong request body")
		_ = fmt.Sprintf("%s", testError)
	})

	t.Run("Unexpected", func(t *testing.T) {
		cases := []struct {
			Name         string
			Resp         *opensearch.Response
			WantedErrors []error
		}{
			{
				Name: "error field object",
				Resp: &opensearch.Response{
					StatusCode: http.StatusForbidden,
					Body:       io.NopCloser(strings.NewReader(`{"error":{"test": "test"},"status":403}`)),
				},
				WantedErrors: []error{opensearch.ErrJSONUnmarshalBody, opensearch.ErrUnknownOpensearchError},
			},
			{
				Name: "unknown json",
				Resp: &opensearch.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`{"_index":"index","_id":"2","matched":false}`)),
				},
				WantedErrors: []error{opensearch.ErrUnknownOpensearchError},
			},
			{
				Name: "io read error",
				Resp: &opensearch.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(iotest.ErrReader(errors.New("io reader test"))),
				},
				WantedErrors: []error{opensearch.ErrReadBody},
			},
			{
				Name:         "empty body",
				Resp:         &opensearch.Response{StatusCode: http.StatusBadRequest},
				WantedErrors: []error{opensearch.ErrUnexpectedEmptyBody},
			},
			{
				Name: "unmarshal error",
				Resp: &opensearch.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`"test"`)),
				},
				WantedErrors: []error{opensearch.ErrJSONUnmarshalBody},
			},
		}

		for _, tt := range cases {
			t.Run(tt.Name, func(t *testing.T) {
				err := opensearch.ParseError(tt.Resp)
				for _, wantedError := range tt.WantedErrors {
					assert.True(t, errors.Is(err, wantedError))
				}
			})
		}

		t.Run("unauthorized", func(t *testing.T) {
			resp := &opensearch.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader(http.StatusText(http.StatusUnauthorized))),
			}
			assert.True(t, resp.IsError())
			err := opensearch.ParseError(resp)
			assert.Equal(t, err.Error(), http.StatusText(http.StatusUnauthorized))
		})
		t.Run("too many requests", func(t *testing.T) {
			resp := &opensearch.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader("429 Too Many Requests /testindex/_bulk")),
			}
			assert.True(t, resp.IsError())
			err := opensearch.ParseError(resp)
			assert.Equal(t, err.Error(), "429 Too Many Requests /testindex/_bulk")
		})
	})
}

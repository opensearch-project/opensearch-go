// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

//nolint:testpackage // need to use the parseError function defined in opensearchapi.go
package opensearchapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v2"
)

func TestError(t *testing.T) {
	t.Run("Error okay", func(t *testing.T) {
		resp := &opensearch.Response{
			StatusCode: http.StatusBadRequest,
			Body: io.NopCloser(
				strings.NewReader(`
					{
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
		err := parseError(resp)
		var testError Error
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

	t.Run("StringError okay", func(t *testing.T) {
		resp := &opensearch.Response{
			StatusCode: http.StatusMethodNotAllowed,
			Body: io.NopCloser(
				strings.NewReader(`
					{
						"error": "Incorrect HTTP method for uri [/_doc] and method [POST], allowed: [HEAD, DELETE, PUT, GET]",
						"status":405
					}`),
			),
		}
		assert.True(t, resp.IsError())
		err := parseError(resp)
		var testError StringError
		require.True(t, errors.As(err, &testError))
		assert.Equal(t, http.StatusMethodNotAllowed, testError.Status)
		assert.Contains(t, testError.Err, "Incorrect HTTP method for uri")
		_ = fmt.Sprintf("%s", testError)
	})
	t.Run("Unexpected", func(t *testing.T) {
		cases := []struct {
			Name         string
			Resp         *opensearch.Response
			WantedErrors []error
		}{
			{
				Name: "response for StringError",
				Resp: &opensearch.Response{
					StatusCode: http.StatusMethodNotAllowed,
					Body:       io.NopCloser(strings.NewReader(`"Test - Trigger an error"`)),
				},
				WantedErrors: []error{ErrJSONUnmarshalBody},
			},
			{
				Name: "response string",
				Resp: &opensearch.Response{
					StatusCode: http.StatusForbidden,
					Body:       io.NopCloser(strings.NewReader(`"Test - Trigger an error"`)),
				},
				WantedErrors: []error{ErrJSONUnmarshalBody},
			},
			{
				Name: "error field string",
				Resp: &opensearch.Response{
					StatusCode: http.StatusForbidden,
					Body:       io.NopCloser(strings.NewReader(`{"error":"test","status":403}`)),
				},
				WantedErrors: []error{ErrJSONUnmarshalBody},
			},
			{
				Name: "error field object",
				Resp: &opensearch.Response{
					StatusCode: http.StatusForbidden,
					Body:       io.NopCloser(strings.NewReader(`{"error":{"test": "test"},"status":403}`)),
				},
				WantedErrors: []error{ErrJSONUnmarshalBody, ErrUnknownOpensearchError},
			},
			{
				Name: "response json",
				Resp: &opensearch.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader(`{"_index":"index","_id":"2","matched":false}`)),
				},
				WantedErrors: []error{ErrJSONUnmarshalBody, ErrJSONUnmarshalBody},
			},
			{
				Name: "io read error",
				Resp: &opensearch.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(iotest.ErrReader(errors.New("io reader test"))),
				},
				WantedErrors: []error{ErrReadBody},
			},
			{
				Name:         "empty body",
				Resp:         &opensearch.Response{StatusCode: http.StatusBadRequest},
				WantedErrors: []error{ErrUnexpectedEmptyBody},
			},
		}

		for _, tt := range cases {
			t.Run(tt.Name, func(t *testing.T) {
				err := parseError(tt.Resp)
				for _, wantedError := range tt.WantedErrors {
					assert.True(t, errors.Is(err, wantedError))
				}
			})
		}
	})
}

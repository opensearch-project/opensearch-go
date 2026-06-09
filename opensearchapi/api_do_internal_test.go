// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/errmask"
)

// doTestReq is a minimal opensearch.Request for driving do() in tests.
type doTestReq struct{}

func (doTestReq) GetRequest(method string) (*http.Request, error) {
	return http.NewRequest(method, "/_test", nil)
}

// doTestTransport is an opensearchtransport.Interface that returns a fixed
// response, mimicking what Perform yields in the default buffered mode: the
// body is an in-memory NopCloser that has already been read once from the wire.
type doTestTransport struct {
	statusCode int
	body       string
}

func (tr doTestTransport) Perform(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: tr.statusCode,
		Status:     http.StatusText(tr.statusCode),
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(tr.body)),
		Request:    &http.Request{URL: &url.URL{Path: "/_test"}},
	}, nil
}

// TestDoErrorBodyReadableNoDecodePath guards the regression flagged in review:
// on the error path with a nil dataPointer, do() must leave the returned
// response body readable rather than draining it to empty. The previous
// implementation called io.Copy(io.Discard, resp.Body) which emptied the
// buffered body, diverging from the ParseError path (dataPointer != nil) that
// re-wraps the bytes.
func TestDoErrorBodyReadableNoDecodePath(t *testing.T) {
	t.Parallel()

	const errBody = `{"status":400,"error":"bad request"}`

	osClient := &opensearch.Client{
		Transport: doTestTransport{statusCode: http.StatusBadRequest, body: errBody},
	}
	c := clientInit(osClient, errmask.Empty)

	resp, err := do[opensearch.NoBody](context.Background(), c, http.MethodGet, doTestReq{}, nil)
	require.Error(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Body, "error-path response body must not be nil")

	got, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.JSONEq(t, errBody, string(got),
		"error-path response body must remain readable, not drained to empty")
}

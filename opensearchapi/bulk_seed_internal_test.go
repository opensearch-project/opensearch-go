// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchapi

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/internal/apiutil"
)

// bulkEchoTransport answers a bulk request with a created item for each action
// in it, and any other request with an empty object.
type bulkEchoTransport struct{}

func (bulkEchoTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	answer := `{}`
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		items := make([]string, bytes.Count(body, []byte(`{"index":`)))
		for i := range items {
			items[i] = `{"index":{"_index":"i","_id":"` + strconv.Itoa(i) + `","result":"created","status":201}}`
		}
		answer = `{"took":1,"errors":false,"items":[` + strings.Join(items, ",") + `]}`
	}

	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Type": {"application/json"}},
		Body:          io.NopCloser(strings.NewReader(answer)),
		ContentLength: int64(len(answer)),
	}, nil
}

func TestBulkSeed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		hint    int // apiutil.WithBulkItems, when not 0
		wantCap int // -1: no seed
	}{
		{name: "a count", hint: 5, wantCap: 5},
		{name: "no count", wantCap: -1},
		{name: "a count that is not one", hint: -3, wantCap: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tt.hint != 0 {
				ctx = apiutil.WithBulkItems(ctx, tt.hint)
			}
			seed := (&Client{}).seedBulk(ctx, BulkReq{})
			if tt.wantCap < 0 {
				require.Nil(t, seed)

				return
			}
			require.NotNil(t, seed)
			require.Empty(t, seed.Items)
			require.Equal(t, tt.wantCap, cap(seed.Items))
			require.True(t, takeBulkSeed(seed.Items), "the seed carries its mark")
			require.False(t, takeBulkSeed(seed.Items), "taken out once")
		})
	}
}

// TestBulkDecodesIntoSeed checks that the generated Bulk decodes the answer into
// the response seedBulk sizes from the count the context gives.
func TestBulkDecodesIntoSeed(t *testing.T) {
	t.Parallel()

	c, err := NewClient(Config{Client: opensearch.Config{
		Addresses: []string{"http://bulk.invalid:9200"},
		Transport: bulkEchoTransport{},
	}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	var body strings.Builder
	for i := range 5 {
		body.WriteString(`{"index":{"_index":"i","_id":"` + strconv.Itoa(i) + `"}}` + "\n{}\n")
	}
	bulk := func(t *testing.T, hinted bool) *BulkResp {
		t.Helper()

		ctx := t.Context()
		if hinted {
			ctx = apiutil.WithBulkItems(ctx, 5)
		}
		resp, err := c.Doc.Bulk(ctx, BulkReq{Body: strings.NewReader(body.String())})
		require.NoError(t, err)
		require.Len(t, resp.Items, 5)

		return resp
	}
	require.Equal(t, 5, cap(bulk(t, true).Items), "the answer filled the seed")
	require.NotEqual(t, 5, cap(bulk(t, false).Items), "without a count, Items grew from empty")
}

// statusTransport answers every request with status and body.
type statusTransport struct {
	status int
	body   string
}

func (s statusTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode:    s.status,
		Header:        http.Header{"Content-Type": {"application/json"}},
		Body:          io.NopCloser(strings.NewReader(s.body)),
		ContentLength: int64(len(s.body)),
	}, nil
}

// TestSeededResponsesWithoutTheirArray checks that a bulk or mget whose response
// was seeded returns what an unseeded one does when the answer does not decode an
// array into the seed: nil Items or Docs, with nothing of the request kept.
func TestSeededResponsesWithoutTheirArray(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "an error status", status: http.StatusInternalServerError, body: `{"error":{"type":"x","reason":"y"},"status":500}`},
		{name: "an empty object", status: http.StatusOK, body: `{}`},
		{name: "no array", status: http.StatusOK, body: `{"took":1,"errors":false}`},
		{name: "a null array", status: http.StatusOK, body: `{"items":null,"docs":null}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := NewClient(Config{Client: opensearch.Config{
				Addresses:    []string{"http://seed.invalid:9200"},
				Transport:    statusTransport{status: tt.status, body: tt.body},
				DisableRetry: true,
			}})
			require.NoError(t, err)
			t.Cleanup(func() { _ = c.Close() })

			bulk, _ := c.Doc.Bulk(apiutil.WithBulkItems(t.Context(), 3), BulkReq{Body: strings.NewReader(`{"index":{"_index":"i"}}` + "\n{}\n")})
			require.NotNil(t, bulk)
			require.Nil(t, bulk.Items)

			req := MGetReq{Index: "i", Body: &MGetBody{Docs: []MGetOperation{{ID: "a"}, {ID: "b"}}}}
			mget, _ := c.Doc.MGet(WithMGetDriver(t.Context(), MGetDriverCoordinator), req)
			require.NotNil(t, mget)
			require.Nil(t, mget.Docs, "nil, so that nothing of the request is kept")
		})
	}
}

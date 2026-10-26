// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/internal/apiutil"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// hintTransport answers bulk requests as a cluster does, and records, for each,
// the actions it carries and the count apiutil.WithBulkItems gave its context.
type hintTransport struct {
	mu struct {
		sync.Mutex
		actions, hints []int
	}
}

func (h *hintTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	answer := `{}`
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		n := bytes.Count(body, []byte(`{"index":`))
		h.mu.Lock()
		h.mu.actions = append(h.mu.actions, n)
		h.mu.hints = append(h.mu.hints, apiutil.BulkItems(req.Context()))
		h.mu.Unlock()

		items := make([]string, n)
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

// TestBulkIndexerSizesTheResponse checks that each bulk request the indexer sends
// gives its context the number of actions it carries, which the API sizes the
// response's Items for.
func TestBulkIndexerSizesTheResponse(t *testing.T) {
	t.Parallel()

	rt := &hintTransport{}
	client, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{
		Addresses: []string{"http://bulk.invalid:9200"},
		Transport: rt,
	}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	bi, err := NewBulkIndexer(BulkIndexerConfig{Client: client, Index: "i"})
	require.NoError(t, err)
	for i := range 7 {
		require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
			Action: "index", DocumentID: strconv.Itoa(i), Body: strings.NewReader(`{}`),
		}))
	}
	require.NoError(t, bi.Close(t.Context()))

	rt.mu.Lock()
	defer rt.mu.Unlock()
	require.NotEmpty(t, rt.mu.actions)
	require.Equal(t, rt.mu.actions, rt.mu.hints)
}

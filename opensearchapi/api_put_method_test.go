// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	opensearch "github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// TestClientSendsPutForCreateOrReplace checks the HTTP method the client
// actually sends for create-or-replace operations.
func TestClientSendsPutForCreateOrReplace(t *testing.T) {
	var (
		mu   sync.Mutex
		seen string
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet { // skip the client's product-check requests
			mu.Lock()
			seen = r.Method
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"acknowledged":true}`))
	}))
	defer ts.Close()

	client, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Addresses: []string{ts.URL}}})
	require.NoError(t, err)
	ctx := context.Background()
	body := func() *strings.Reader { return strings.NewReader("{}") }

	tests := []struct {
		name string
		call func() error
		want string
	}{
		{"index with id", func() error {
			_, err := client.Document.Index(ctx, opensearchapi.IndexReq{Index: "i", ID: "1", Body: body()})
			return err
		}, http.MethodPut},
		{"index without id", func() error {
			_, err := client.Document.Index(ctx, opensearchapi.IndexReq{Index: "i", Body: body()})
			return err
		}, http.MethodPost},
		{"create", func() error {
			_, err := client.Document.Create(ctx, opensearchapi.CreateReq{Index: "i", ID: "1", Body: body()})
			return err
		}, http.MethodPut},
		{"put mapping", func() error {
			_, err := client.Indices.PutMapping(ctx, &opensearchapi.IndicesPutMappingReq{Indices: []string{"i"}, BodyReader: body()})
			return err
		}, http.MethodPut},
		{"put script", func() error {
			_, err := client.PutScript(ctx, opensearchapi.PutScriptReq{ID: "s", BodyReader: body()})
			return err
		}, http.MethodPut},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mu.Lock()
			seen = ""
			mu.Unlock()
			_ = tt.call() // response shape is irrelevant; only the method matters
			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, tt.want, seen)
		})
	}
}

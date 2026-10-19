// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
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
	var seen struct {
		sync.Mutex
		method string
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet { // skip the client's health-check and discovery requests
			seen.Lock()
			seen.method = r.Method
			seen.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"acknowledged":true}`))
	}))
	defer ts.Close()

	client, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Addresses: []string{ts.URL}}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	ctx := t.Context()
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
		{"mapping put", func() error {
			_, err := client.Indices.Mapping.Put(ctx, &opensearchapi.IndicesPutMappingReq{Indices: []string{"i"}, BodyReader: body()})
			return err
		}, http.MethodPut},
		{"put script", func() error {
			_, err := client.PutScript(ctx, opensearchapi.PutScriptReq{ID: "s", BodyReader: body()})
			return err
		}, http.MethodPut},
		{"clone", func() error {
			_, err := client.Indices.Clone(ctx, opensearchapi.IndicesCloneReq{Index: "i", Target: "t", BodyReader: body()})
			return err
		}, http.MethodPut},
		{"shrink", func() error {
			_, err := client.Indices.Shrink(ctx, opensearchapi.IndicesShrinkReq{Index: "i", Target: "t", BodyReader: body()})
			return err
		}, http.MethodPut},
		{"split", func() error {
			_, err := client.Indices.Split(ctx, opensearchapi.IndicesSplitReq{Index: "i", Target: "t", BodyReader: body()})
			return err
		}, http.MethodPut},
		{"snapshot create", func() error {
			_, err := client.Snapshot.Create(ctx, opensearchapi.SnapshotCreateReq{Repository: "r", Snapshot: "s", BodyReader: body()})
			return err
		}, http.MethodPut},
		{"snapshot create repository", func() error {
			_, err := client.Snapshot.CreateRepository(ctx, opensearchapi.SnapshotCreateRepositoryReq{Repository: "r", BodyReader: body()})
			return err
		}, http.MethodPut},
		{"repository create", func() error {
			_, err := client.Snapshot.Repository.Create(ctx, opensearchapi.SnapshotCreateRepositoryReq{Repository: "r", BodyReader: body()})
			return err
		}, http.MethodPut},
		{"put component template", func() error {
			_, err := client.Cluster.PutComponentTemplate(ctx, opensearchapi.ClusterPutComponentTemplateReq{Name: "n", BodyReader: body()})
			return err
		}, http.MethodPut},
		{"put index template", func() error {
			_, err := client.Indices.PutIndexTemplate(ctx, opensearchapi.IndicesPutIndexTemplateReq{Name: "n", BodyReader: body()})
			return err
		}, http.MethodPut},
		{"put template", func() error {
			_, err := client.Indices.PutTemplate(ctx, opensearchapi.IndicesPutTemplateReq{Name: "n", BodyReader: body()})
			return err
		}, http.MethodPut},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seen.Lock()
			seen.method = ""
			seen.Unlock()
			_ = tt.call() // response shape is irrelevant; only the method matters
			seen.Lock()
			defer seen.Unlock()
			require.Equal(t, tt.want, seen.method)
		})
	}
}

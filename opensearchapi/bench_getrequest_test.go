// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"net/http"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

func BenchmarkGetRequest_Search(b *testing.B) {
	req := opensearchapi.SearchReq{
		Indices: []string{"my-index"},
	}
	b.ReportAllocs()
	for b.Loop() {
		r, _ := req.GetRequest(http.MethodPost)
		_ = r
	}
}

func BenchmarkGetRequest_IndicesCreate(b *testing.B) {
	req := opensearchapi.IndicesCreateReq{
		Index: "my-index",
	}
	b.ReportAllocs()
	for b.Loop() {
		r, _ := req.GetRequest(http.MethodPut)
		_ = r
	}
}

func BenchmarkGetRequest_DocumentGet(b *testing.B) {
	req := opensearchapi.DocumentGetReq{
		Index:      "my-index",
		DocumentID: "doc-1",
	}
	b.ReportAllocs()
	for b.Loop() {
		r, _ := req.GetRequest(http.MethodGet)
		_ = r
	}
}

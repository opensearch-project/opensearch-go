// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchapi_test

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// Refresh sets the refresh query parameter with a typed constant.
func ExampleRefresh() {
	req, err := opensearchapi.IndexReq{
		Index:  "logs",
		ID:     "1",
		Body:   strings.NewReader(`{}`),
		Params: &opensearchapi.IndexParams{Refresh: opensearchapi.RefreshWaitFor},
	}.GetRequest(http.MethodPut)
	if err != nil {
		panic(err)
	}
	fmt.Println(req.URL.RawQuery)
	// Output: refresh=wait_for
}

// ExpandWildcard lists the wildcard expansion modes; multiple values are
// sent comma-separated.
func ExampleExpandWildcard() {
	req, err := opensearchapi.SearchReq{
		Indices: []string{"logs-*"},
		Params: &opensearchapi.SearchParams{
			ExpandWildcards: []opensearchapi.ExpandWildcard{
				opensearchapi.ExpandWildcardOpen,
				opensearchapi.ExpandWildcardHidden,
			},
		},
	}.GetRequest(http.MethodPost)
	if err != nil {
		panic(err)
	}
	fmt.Println(req.URL.Path)
	fmt.Println(req.URL.RawQuery)
	// Output:
	// /logs-*/_search
	// expand_wildcards=open%2Chidden
}

// WaitForActiveShardsCount waits for a specific number of active shard copies.
func ExampleWaitForActiveShards() {
	req, err := opensearchapi.IndexReq{
		Index:  "logs",
		ID:     "1",
		Body:   strings.NewReader(`{}`),
		Params: &opensearchapi.IndexParams{WaitForActiveShards: opensearchapi.WaitForActiveShardsCount(2)},
	}.GetRequest(http.MethodPut)
	if err != nil {
		panic(err)
	}
	fmt.Println(req.URL.RawQuery)
	// Output: wait_for_active_shards=2
}

// WaitForActiveShardsAll waits for every copy of each shard to be active.
func ExampleWaitForActiveShards_all() {
	req, err := opensearchapi.IndexReq{
		Index:  "logs",
		ID:     "1",
		Body:   strings.NewReader(`{}`),
		Params: &opensearchapi.IndexParams{WaitForActiveShards: opensearchapi.WaitForActiveShardsAll},
	}.GetRequest(http.MethodPut)
	if err != nil {
		panic(err)
	}
	fmt.Println(req.URL.RawQuery)
	// Output: wait_for_active_shards=all
}

// DurationUnbounded sends a duration parameter as -1, the server's value for
// "no limit" or "disabled" on parameters that accept it. A zero time.Duration
// leaves the parameter unset, so it is omitted from the query string.
func ExampleSearchParams_durationUnbounded() {
	req, err := opensearchapi.SearchReq{
		Indices: []string{"logs"},
		Params:  &opensearchapi.SearchParams{Scroll: opensearchapi.DurationUnbounded},
	}.GetRequest(http.MethodPost)
	if err != nil {
		panic(err)
	}
	fmt.Println(req.URL.RawQuery)
	// Output: scroll=-1
}

// CatRespFormat selects a cat API's response format; CatRespFormatJSON asks for
// JSON.
func ExampleCatRespFormat() {
	req, err := opensearchapi.CatIndicesReq{
		Params: &opensearchapi.CatIndicesParams{
			DebugParams: opensearchapi.DebugParams{Format: opensearchapi.CatRespFormatJSON},
		},
	}.GetRequest(http.MethodGet)
	if err != nil {
		panic(err)
	}
	fmt.Println(req.URL.Path)
	fmt.Println(req.URL.RawQuery)
	// Output:
	// /_cat/indices
	// format=json
}

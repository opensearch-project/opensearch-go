// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// IndicesShardStoresReq represents possible options for the index shrink request
type IndicesShardStoresReq struct {
	Indices []string

	Header http.Header
	Params IndicesShardStoresParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IndicesShardStoresReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.PrefixActionPath{Prefix: opensearch.Prefix(strings.Join(r.Indices, ",")), Action: "_shard_stores"}.Build()
	if err != nil {
		return nil, err
	}
	return opensearch.BuildRequest(http.MethodGet, path, nil, r.Params.get(), r.Header)
}

// IndicesShardStoresResp represents the returned struct of the index shrink response
type IndicesShardStoresResp struct {
	Indices map[string]struct {
		Shards map[string]struct {
			Stores []json.RawMessage `json:"stores"`
		} `json:"shards"`
	} `json:"indices"`
	Failures []FailuresShard `json:"failures"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r IndicesShardStoresResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

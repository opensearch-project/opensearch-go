// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/internal/build"
	ospath "github.com/opensearch-project/opensearch-go/v4/internal/path"
)

// IndicesRefreshReq represents possible options for the <index>/_refresh request
type IndicesRefreshReq struct {
	Indices []string

	// Deprecated: use Indices.
	Index []string

	Header http.Header
	Params IndicesRefreshParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IndicesRefreshReq) GetRequest(method string) (*http.Request, error) {
	indices := r.Indices
	if len(indices) == 0 {
		indices = r.Index
	}
	path, err := ospath.IndicesRefreshPath{Index: indices}.Build()
	if err != nil {
		return nil, err
	}
	return build.Request(method, path, nil, r.Params.get(), r.Header)
}

// IndicesRefreshResp represents the returned struct of the indices refresh response
type IndicesRefreshResp struct {
	Shards   ResponseShards `json:"_shards"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r IndicesRefreshResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// IndicesDeleteReq represents possible options for the delete indices request
type IndicesDeleteReq struct {
	Indices []string
	Header  http.Header
	Params  IndicesDeleteParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IndicesDeleteReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.IndexPath{Index: opensearch.Index(strings.Join(r.Indices, ","))}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(http.MethodDelete, path, nil, r.Params.get(), r.Header)
}

// IndicesDeleteResp represents the returned struct of the delete indices response
type IndicesDeleteResp struct {
	Acknowledged bool `json:"acknowledged"`
	response     *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r IndicesDeleteResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

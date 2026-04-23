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

// IndicesExistsReq represents possible options for the index exists request
type IndicesExistsReq struct {
	Indices []string
	Header  http.Header
	Params  IndicesExistsParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IndicesExistsReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.IndexPath{Index: opensearch.Index(strings.Join(r.Indices, ","))}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(http.MethodHead, path, nil, r.Params.get(), r.Header)
}

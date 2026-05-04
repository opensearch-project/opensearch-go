// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// IndexTemplateExistsReq represents possible options for the index create request
type IndexTemplateExistsReq struct {
	IndexTemplate string

	Header http.Header
	Params IndexTemplateExistsParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IndexTemplateExistsReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.ResourcePath{Prefix: "_index_template", Name: opensearch.Name(r.IndexTemplate)}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(http.MethodHead, path, nil, r.Params.get(), r.Header)
}

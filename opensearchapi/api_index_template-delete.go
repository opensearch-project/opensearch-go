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

// IndexTemplateDeleteReq represents possible options for the index create request
type IndexTemplateDeleteReq struct {
	IndexTemplate string

	Header http.Header
	Params IndexTemplateDeleteParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IndexTemplateDeleteReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.ResourcePath{Prefix: "_index_template", Name: opensearch.Name(r.IndexTemplate)}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(http.MethodDelete, path, nil, r.Params.get(), r.Header)
}

// IndexTemplateDeleteResp represents the returned struct of the index create response
type IndexTemplateDeleteResp struct {
	Acknowledged bool `json:"acknowledged"`
	response     *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r IndexTemplateDeleteResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

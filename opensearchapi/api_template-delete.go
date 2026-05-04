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

// TemplateDeleteReq represents possible options for the index create request
type TemplateDeleteReq struct {
	Template string

	Header http.Header
	Params TemplateDeleteParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r TemplateDeleteReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.ResourcePath{Prefix: "_template", Name: opensearch.Name(r.Template)}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(http.MethodDelete, path, nil, r.Params.get(), r.Header)
}

// TemplateDeleteResp represents the returned struct of the index create response
type TemplateDeleteResp struct {
	Acknowledged bool `json:"acknowledged"`
	response     *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r TemplateDeleteResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

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

// TemplateExistsReq represents possible options for the index create request
type TemplateExistsReq struct {
	Template string

	Header http.Header
	Params TemplateExistsParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r TemplateExistsReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.ResourcePath{Prefix: "_template", Name: opensearch.Name(r.Template)}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(http.MethodHead, path, nil, r.Params.get(), r.Header)
}

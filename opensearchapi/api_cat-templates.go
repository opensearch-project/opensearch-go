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

// CatTemplatesReq represent possible options for the /_cat/templates request
type CatTemplatesReq struct {
	Templates []string
	Header    http.Header
	Params    CatTemplatesParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r CatTemplatesReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.ActionSuffixPath{Action: "_cat/templates", Suffix: opensearch.Suffix(strings.Join(r.Templates, ","))}.Build()
	if err != nil {
		return nil, err
	}
	return opensearch.BuildRequest(http.MethodGet, path, nil, r.Params.get(), r.Header)
}

// CatTemplatesResp represents the returned struct of the /_cat/templates response
type CatTemplatesResp struct {
	Templates []CatTemplateResp
	response  *opensearch.Response
}

// CatTemplateResp represents one index of the CatTemplatesResp
type CatTemplateResp struct {
	Name          string  `json:"name"`
	IndexPatterns string  `json:"index_patterns"`
	Order         int     `json:"order,string"`
	Version       *string `json:"version"`
	ComposedOf    string  `json:"composed_of"`
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r CatTemplatesResp) Inspect() Inspect {
	return Inspect{
		Response: r.response,
	}
}

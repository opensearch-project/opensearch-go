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

// TemplateGetReq represents possible options for the index create request
type TemplateGetReq struct {
	Templates []string

	Header http.Header
	Params TemplateGetParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r TemplateGetReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.ActionSuffixPath{
		Action: "_template",
		Suffix: opensearch.Suffix(strings.Join(r.Templates, ",")),
	}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(http.MethodGet, path, nil, r.Params.get(), r.Header)
}

// TemplateGetResp represents the returned struct of the index create response
type TemplateGetResp struct {
	Templates map[string]TemplateGetDetails
	response  *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r TemplateGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// TemplateGetDetails is a sub type of TemplateGetResp containing information about an index template
type TemplateGetDetails struct {
	Order         int64           `json:"order"`
	Version       int64           `json:"version"`
	IndexPatterns []string        `json:"index_patterns"`
	Mappings      json.RawMessage `json:"mappings"`
	Settings      json.RawMessage `json:"settings"`
	Aliases       json.RawMessage `json:"aliases"`
}

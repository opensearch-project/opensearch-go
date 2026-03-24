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

// CatFieldDataReq represent possible options for the /_cat/fielddata request
type CatFieldDataReq struct {
	FieldData []string
	Header    http.Header
	Params    CatFieldDataParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r CatFieldDataReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.ActionSuffixPath{Action: "_cat/fielddata", Suffix: opensearch.Suffix(strings.Join(r.FieldData, ","))}.Build()
	if err != nil {
		return nil, err
	}
	return opensearch.BuildRequest(http.MethodGet, path, nil, r.Params.get(), r.Header)
}

// CatFieldDataResp represents the returned struct of the /_cat/fielddata response
type CatFieldDataResp struct {
	FieldData []CatFieldDataItemResp
	response  *opensearch.Response
}

// CatFieldDataItemResp represents one index of the CatFieldDataResp
type CatFieldDataItemResp struct {
	ID    string `json:"id"`
	Host  string `json:"host"`
	IP    string `json:"ip"`
	Node  string `json:"node"`
	Field string `json:"field"`
	Size  string `json:"size"`
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r CatFieldDataResp) Inspect() Inspect {
	return Inspect{
		Response: r.response,
	}
}

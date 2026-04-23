// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"encoding/json"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// DocumentSourceReq represents possible options for the /<Index>/_source/<DocumentID> get request
type DocumentSourceReq struct {
	Index      string
	DocumentID string

	Header http.Header
	Params DocumentSourceParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r DocumentSourceReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.DocumentPath{
		Index:      opensearch.Index(r.Index),
		Action:     "_source",
		DocumentID: opensearch.DocumentID(r.DocumentID),
	}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(http.MethodGet, path, nil, r.Params.get(), r.Header)
}

// DocumentSourceResp represents the returned struct of the /<Index>/_source/<DocumentID> get response
type DocumentSourceResp struct {
	Source   json.RawMessage
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r DocumentSourceResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

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

// DocumentExistsReq represents possible options for the document exists request
type DocumentExistsReq struct {
	Index      string
	DocumentID string

	Header http.Header
	Params DocumentExistsParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r DocumentExistsReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.DocumentPath{
		Index:      opensearch.Index(r.Index),
		Action:     "_doc",
		DocumentID: opensearch.DocumentID(r.DocumentID),
	}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(http.MethodHead, path, nil, r.Params.get(), r.Header)
}

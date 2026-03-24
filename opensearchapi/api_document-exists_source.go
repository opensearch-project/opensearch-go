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

// DocumentExistsSourceReq represents possible options for the _source exists request
type DocumentExistsSourceReq struct {
	Index      string
	DocumentID string

	Header http.Header
	Params DocumentExistsSourceParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r DocumentExistsSourceReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.DocumentPath{
		Index:      opensearch.Index(r.Index),
		Action:     "_source",
		DocumentID: opensearch.DocumentID(r.DocumentID),
	}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(http.MethodHead, path, nil, r.Params.get(), r.Header)
}

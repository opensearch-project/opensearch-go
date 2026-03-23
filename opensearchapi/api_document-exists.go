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
	return opensearch.BuildRequest(
		"HEAD",
		opensearch.BuildPath(r.Index, "_doc", r.DocumentID),
		nil,
		r.Params.get(),
		r.Header,
	)
}

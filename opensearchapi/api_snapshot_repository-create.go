// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"fmt"
	"io"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v3"
)

// SnapshotRepositoryCreateReq represents possible options for the index create request
type SnapshotRepositoryCreateReq struct {
	Repo string

	Body io.Reader

	Header http.Header
	Params SnapshotRepositoryCreateParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r SnapshotRepositoryCreateReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"PUT",
		fmt.Sprintf("/_snapshot/%s", r.Repo),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// SnapshotRepositoryCreateResp represents the returned struct of the index create response
type SnapshotRepositoryCreateResp struct {
	Acknowledged bool `json:"acknowledged"`
	response     *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r SnapshotRepositoryCreateResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

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

// IngestDeleteReq represents possible options for the index create request
type IngestDeleteReq struct {
	PipelineID string

	Header http.Header
	Params IngestDeleteParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IngestDeleteReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.ResourceActionPath{Prefix: "_ingest", Name: "pipeline", Action: opensearch.Action(r.PipelineID)}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(http.MethodDelete, path, nil, r.Params.get(), r.Header)
}

// IngestDeleteResp represents the returned struct of the index create response
type IngestDeleteResp struct {
	Acknowledged bool `json:"acknowledged"`
	response     *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r IngestDeleteResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"io"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// DocumentCreateReq represents possible options for the /<index>/_create request
type DocumentCreateReq struct {
	Index      string
	DocumentID string

	Body io.Reader

	Header http.Header
	Params DocumentCreateParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r DocumentCreateReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"PUT",
		opensearch.BuildPath(r.Index, "_create", r.DocumentID),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// DocumentCreateResp represents the returned struct of the /_doc response
type DocumentCreateResp struct {
	Index         string         `json:"_index"`
	ID            string         `json:"_id"`
	Version       int            `json:"_version"`
	Result        string         `json:"result"`
	Type          string         `json:"_type,omitempty"` // Deprecated: ES 6.0, removed in OS 2.0
	ForcedRefresh bool           `json:"forced_refresh"`
	Shards        ResponseShards `json:"_shards"`
	SeqNo         int            `json:"_seq_no"`
	PrimaryTerm   int            `json:"_primary_term"`
	response      *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r DocumentCreateResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

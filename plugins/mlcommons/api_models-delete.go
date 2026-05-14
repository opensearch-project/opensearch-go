// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mlcommons

import (
	"fmt"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// ModelsDeleteReq represents the delete model request.
//
// Reference: https://docs.opensearch.org/latest/ml-commons-plugin/api/model-apis/delete-model/
type ModelsDeleteReq struct {
	ModelID string

	Params ModelsDeleteParams
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ModelsDeleteReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		http.MethodDelete,
		fmt.Sprintf("/_plugins/_ml/models/%s", r.ModelID),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// ModelsDeleteResp represents the delete model response.
type ModelsDeleteResp struct {
	Index   string `json:"_index,omitempty"`
	ID      string `json:"_id,omitempty"`
	Version int    `json:"_version,omitempty"`
	Result  string `json:"result,omitempty"`
	Shards  *struct {
		Total      int `json:"total"`
		Successful int `json:"successful"`
		Failed     int `json:"failed"`
	} `json:"_shards,omitempty"`
	SeqNo       int `json:"_seq_no,omitempty"`
	PrimaryTerm int `json:"_primary_term,omitempty"`
	response    *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r ModelsDeleteResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

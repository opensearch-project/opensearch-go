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

// TasksDeleteReq represents the delete ML task request.
//
// Reference: https://docs.opensearch.org/latest/ml-commons-plugin/api/tasks-apis/delete-task/
type TasksDeleteReq struct {
	TaskID string

	Params TasksDeleteParams
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r TasksDeleteReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		http.MethodDelete,
		fmt.Sprintf("/_plugins/_ml/tasks/%s", r.TaskID),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// TasksDeleteResp represents the delete ML task response.
type TasksDeleteResp struct {
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
func (r TasksDeleteResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

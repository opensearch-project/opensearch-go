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

// TasksGetReq represents the get ML task request.
//
// Reference: https://docs.opensearch.org/latest/ml-commons-plugin/api/tasks-apis/get-task/
type TasksGetReq struct {
	TaskID string

	Params TasksGetParams
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r TasksGetReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		http.MethodGet,
		fmt.Sprintf("/_plugins/_ml/tasks/%s", r.TaskID),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// TasksGetResp represents the get ML task response.
type TasksGetResp struct {
	ModelID        string   `json:"model_id,omitempty"`
	TaskType       string   `json:"task_type,omitempty"`
	FunctionName   string   `json:"function_name,omitempty"`
	State          string   `json:"state,omitempty"`
	WorkerNode     []string `json:"worker_node,omitempty"`
	CreateTime     *int64   `json:"create_time,omitempty"`
	LastUpdateTime *int64   `json:"last_update_time,omitempty"`
	Error          string   `json:"error,omitempty"`
	IsAsync        *bool    `json:"is_async,omitempty"`
	response       *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r TasksGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

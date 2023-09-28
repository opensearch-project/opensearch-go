// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package opensearchapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v2"
)

// TasksGetReq represents possible options for the index create request
type TasksGetReq struct {
	TaskID string

	Header http.Header
	Params TasksGetParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r TasksGetReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		fmt.Sprintf("/_tasks/%s", r.TaskID),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// TasksGetResp represents the returned struct of the index create response
type TasksGetResp struct {
	Completed bool `json:"completed"`
	Task      struct {
		Node               string          `json:"node"`
		ID                 int             `json:"id"`
		Type               string          `json:"type"`
		Action             string          `json:"action"`
		Description        string          `json:"description"`
		StartTimeInMillis  int64           `json:"start_time_in_millis"`
		RunningTimeInNanos int64           `json:"running_time_in_nanos"`
		Cancellable        bool            `json:"cancellable"`
		Cancelled          bool            `json:"cancelled"`
		Headers            json.RawMessage `json:"headers"`
		ResourceStats      struct {
			Average struct {
				CPUTimeInNanos int `json:"cpu_time_in_nanos"`
				MemoryInBytes  int `json:"memory_in_bytes"`
			} `json:"average"`
			Total struct {
				CPUTimeInNanos int `json:"cpu_time_in_nanos"`
				MemoryInBytes  int `json:"memory_in_bytes"`
			} `json:"total"`
			Min struct {
				CPUTimeInNanos int `json:"cpu_time_in_nanos"`
				MemoryInBytes  int `json:"memory_in_bytes"`
			} `json:"min"`
			Max struct {
				CPUTimeInNanos int `json:"cpu_time_in_nanos"`
				MemoryInBytes  int `json:"memory_in_bytes"`
			} `json:"max"`
			ThreadInfo struct {
				ThreadExecutions int `json:"thread_executions"`
				ActiveThreads    int `json:"active_threads"`
			} `json:"thread_info"`
		} `json:"resource_stats"`
	} `json:"task"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r TasksGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// TasksGetDetails is a sub type of TasksGetResp containing information about an index template
type TasksGetDetails struct {
	Order         int64           `json:"order"`
	Version       int64           `json:"version"`
	IndexPatterns []string        `json:"index_patterns"`
	Mappings      json.RawMessage `json:"mappings"`
	Settings      json.RawMessage `json:"settings"`
	Aliases       json.RawMessage `json:"aliases"`
}

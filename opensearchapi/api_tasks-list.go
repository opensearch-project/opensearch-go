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
	"net/http"

	"github.com/opensearch-project/opensearch-go/v2"
)

// TasksListReq represents possible options for the index create request
type TasksListReq struct {
	Header http.Header
	Params TasksListParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r TasksListReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		"/_tasks",
		nil,
		r.Params.get(),
		r.Header,
	)
}

// TasksListResp represents the returned struct of the index create response
type TasksListResp struct {
	Nodes    map[string]TasksListNodes `json:"nodes"`
	Tasks    map[string]TasksListTask  `json:"tasks"` // tasks is returned when group_by is set to none or parents
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r TasksListResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// TasksListNodes is a sub type of TaskListResp containing information about a node and the tasks running on it
type TasksListNodes struct {
	Name             string                   `json:"name"`
	TransportAddress string                   `json:"transport_address"`
	Host             string                   `json:"host"`
	IP               string                   `json:"ip"`
	Roles            []string                 `json:"roles"`
	Attributes       map[string]string        `json:"attributes"`
	Tasks            map[string]TasksListTask `json:"tasks"`
}

// TasksListTask is a sub type of TaskListResp, TaskListNodes containing information about a task
type TasksListTask struct {
	Node               string                 `json:"node"`
	ID                 int                    `json:"id"`
	Type               string                 `json:"type"`
	Action             string                 `json:"action"`
	Description        string                 `json:"description"`
	StartTimeInMillis  int64                  `json:"start_time_in_millis"`
	RunningTimeInNanos int64                  `json:"running_time_in_nanos"`
	Cancellable        bool                   `json:"cancellable"`
	Cancelled          bool                   `json:"cancelled"`
	Headers            map[string]string      `json:"headers"`
	ResourceStats      TasksListResourceStats `json:"resource_stats"`
	ParentTaskID       string                 `json:"parent_task_id"`
	Children           []TasksListTask        `json:"children,omitempty"`
}

// TasksListResourceStats is a sub type of TaskListTask containing information about task stats
type TasksListResourceStats struct {
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
}

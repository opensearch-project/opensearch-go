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
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
)

// TasksCancelReq represents possible options for the index create request
type TasksCancelReq struct {
	TaskID string

	Header http.Header
	Params TasksCancelParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r TasksCancelReq) GetRequest() (*http.Request, error) {
	var path strings.Builder
	path.Grow(len("/_tasks//_cancel") + len(r.TaskID))
	path.WriteString("/_tasks")
	if r.TaskID != "" {
		path.WriteString("/")
		path.WriteString(r.TaskID)
	}
	path.WriteString("/_cancel")
	return opensearch.BuildRequest(
		"POST",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// TasksCancelResp represents the returned struct of the index create response
type TasksCancelResp struct {
	Nodes    map[string]TaskCancel `json:"nodes"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r TasksCancelResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// TaskCancel is a sub type of TaskCancelResp containing information about a node the task was running on
type TaskCancel struct {
	Name             string                    `json:"name"`
	TransportAddress string                    `json:"transport_address"`
	Host             string                    `json:"host"`
	IP               string                    `json:"ip"`
	Roles            []string                  `json:"roles"`
	Attributes       map[string]string         `json:"attributes"`
	Tasks            map[string]TaskCancelInfo `json:"tasks"`
}

// TaskCancelInfo is a sub type of TaskCancle containing information about the canceled task
type TaskCancelInfo struct {
	Node                   string          `json:"node"`
	ID                     int             `json:"id"`
	Type                   string          `json:"type"`
	Action                 string          `json:"action"`
	StartTimeInMillis      int64           `json:"start_time_in_millis"`
	RunningTimeInNanos     int             `json:"running_time_in_nanos"`
	CancellationTimeMillis int64           `json:"cancellation_time_millis"`
	Cancellable            bool            `json:"cancellable"`
	Cancelled              bool            `json:"cancelled"`
	Headers                json.RawMessage `json:"headers"`
}

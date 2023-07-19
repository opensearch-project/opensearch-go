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

// CatPendingTasksReq represent possible options for the /_cat/pending_tasks request
type CatPendingTasksReq struct {
	Header http.Header
	Params CatPendingTasksParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r CatPendingTasksReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		"/_cat/pending_tasks",
		nil,
		r.Params.get(),
		r.Header,
	)
}

// CatPendingTasksResp represents the returned struct of the /_cat/pending_tasks response
type CatPendingTasksResp struct {
	PendingTasks []CatPendingTaskResp
	response     *opensearch.Response
}

// CatPendingTaskResp represents one index of the CatPendingTasksResp
type CatPendingTaskResp struct {
	InsertOrder string `json:"insertOrder"`
	TimeInQueue string `json:"timeInQueue"`
	Priority    string `json:"priority"`
	Source      string `json:"source"`
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r CatPendingTasksResp) Inspect() Inspect {
	return Inspect{
		Response: r.response,
	}
}

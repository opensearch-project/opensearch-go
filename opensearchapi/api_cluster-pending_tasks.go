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

// ClusterPendingTasksReq represents possible options for the /_cluster/pending_tasks request
type ClusterPendingTasksReq struct {
	Header http.Header
	Params ClusterPendingTasksParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ClusterPendingTasksReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		"/_cluster/pending_tasks",
		nil,
		r.Params.get(),
		r.Header,
	)
}

// ClusterPendingTasksResp represents the returned struct of the  ClusterPendingTasksReq response
type ClusterPendingTasksResp struct {
	Tasks    []ClusterPendingTasksItem `json:"tasks"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r ClusterPendingTasksResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// ClusterPendingTasksItem is a sub type if ClusterPendingTasksResp containing information about a task
type ClusterPendingTasksItem struct {
	InsertOrder       int    `json:"insert_order"`
	Priority          string `json:"priority"`
	Source            string `json:"source"`
	TimeInQueueMillis int    `json:"time_in_queue_millis"`
	TimeInQueue       string `json:"time_in_queue"`
	Executing         bool   `json:"executing"`
}

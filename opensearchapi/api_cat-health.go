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

// CatHealthReq represent possible options for the /_cat/health request
type CatHealthReq struct {
	Header http.Header
	Params CatHealthParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r CatHealthReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		"/_cat/health",
		nil,
		r.Params.get(),
		r.Header,
	)
}

// CatHealthResp represents the returned struct of the /_cat/health response
type CatHealthResp struct {
	Health   []CatHealthItemResp
	response *opensearch.Response
}

// CatHealthItemResp represents one index of the CatHealthResp
type CatHealthItemResp struct {
	Epoch                    int    `json:"epoch,string"`
	Timestamp                string `json:"timestamp"`
	Cluster                  string `json:"cluster"`
	Status                   string `json:"status"`
	NodeTotal                int    `json:"node.total,string"`
	NodeData                 int    `json:"node.data,string"`
	DiscoveredMaster         bool   `json:"discovered_master,string"`
	DiscoveredClusterManager bool   `json:"discovered_cluster_manager,string"`
	Shards                   int    `json:"shards,string"`
	Primary                  int    `json:"pri,string"`
	Relocating               int    `json:"relo,string"`
	Initializing             int    `json:"init,string"`
	Unassigned               int    `json:"unassign,string"`
	PendingTasks             int    `json:"pending_tasks,string"`
	MaxTaskWaitTime          string `json:"max_task_wait_time"`
	ActiveShardsPercent      string `json:"active_shards_percent"`
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r CatHealthResp) Inspect() Inspect {
	return Inspect{
		Response: r.response,
	}
}

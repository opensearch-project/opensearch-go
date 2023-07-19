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
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
)

// CatThreadPoolReq represent possible options for the /_cat/thread_pool request
type CatThreadPoolReq struct {
	Pools  []string
	Header http.Header
	Params CatThreadPoolParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r CatThreadPoolReq) GetRequest() (*http.Request, error) {
	pools := strings.Join(r.Pools, ",")
	var path strings.Builder
	path.Grow(len("/_cat/thread_pool/") + len(pools))
	path.WriteString("/_cat/thread_pool")
	if len(r.Pools) > 0 {
		path.WriteString("/")
		path.WriteString(pools)
	}
	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// CatThreadPoolResp represents the returned struct of the /_cat/thread_pool response
type CatThreadPoolResp struct {
	ThreadPool []CatThreadPoolItemResp
	response   *opensearch.Response
}

// CatThreadPoolItemResp represents one index of the CatThreadPoolResp
type CatThreadPoolItemResp struct {
	NodeName        string  `json:"node_name"`
	NodeID          string  `json:"node_id"`
	EphemeralNodeID string  `json:"ephemeral_node_id"`
	PID             int     `json:"pid,string"`
	Host            string  `json:"host"`
	IP              string  `json:"ip"`
	Port            int     `json:"port,string"`
	Name            string  `json:"name"`
	Type            string  `json:"type"`
	Active          int     `json:"active,string"`
	PoolSize        int     `json:"pool_size,string"`
	Queue           int     `json:"queue,string"`
	QueueSize       int     `json:"queue_size,string"`
	Rejected        int     `json:"rejected,string"`
	Largest         int     `json:"largest,string"`
	Completed       int     `json:"completed,string"`
	Core            *int    `json:"core,string"`
	Max             *int    `json:"max,string"`
	Size            *int    `json:"size,string"`
	KeepAlive       *string `json:"keep_alive"`
	TotalWaitTime   string  `json:"total_wait_time"`
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r CatThreadPoolResp) Inspect() Inspect {
	return Inspect{
		Response: r.response,
	}
}

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

// CatAllocationReq represent possible options for the /_cat/allocation request
type CatAllocationReq struct {
	NodeIDs []string
	Header  http.Header
	Params  CatAllocationParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r CatAllocationReq) GetRequest() (*http.Request, error) {
	nodes := strings.Join(r.NodeIDs, ",")
	var path strings.Builder
	path.Grow(len("/_cat/allocation/") + len(nodes))
	path.WriteString("/_cat/allocation")
	if len(r.NodeIDs) > 0 {
		path.WriteString("/")
		path.WriteString(nodes)
	}
	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// CatAllocationsResp represents the returned struct of the /_cat/allocation response
type CatAllocationsResp struct {
	Allocations []CatAllocationResp
	response    *opensearch.Response
}

// CatAllocationResp represents one index of the CatAllocationResp
type CatAllocationResp struct {
	Shards int `json:"shards,string"`
	// Pointer of string as the api can returns null for those fileds with Node set to "UNASSIGNED"
	DiskIndices *string `json:"disk.indices"`
	DiskUsed    *string `json:"disk.used"`
	DiskAvail   *string `json:"disk.avail"`
	DiskTotal   *string `json:"disk.total"`
	DiskPercent *int    `json:"disk.percent,string"`
	Host        *string `json:"host"`
	IP          *string `json:"ip"`
	Node        string  `json:"node"`
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r CatAllocationsResp) Inspect() Inspect {
	return Inspect{
		Response: r.response,
	}
}

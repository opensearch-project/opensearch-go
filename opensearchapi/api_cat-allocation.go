// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// CatAllocationReq represent possible options for the /_cat/allocation request
type CatAllocationReq struct {
	NodeIDs []string
	Header  http.Header
	Params  CatAllocationParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r CatAllocationReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.ActionSuffixPath{Action: "_cat/allocation", Suffix: opensearch.Suffix(strings.Join(r.NodeIDs, ","))}.Build()
	if err != nil {
		return nil, err
	}
	return opensearch.BuildRequest(http.MethodGet, path, nil, r.Params.get(), r.Header)
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

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r CatAllocationsResp) Inspect() Inspect {
	return Inspect{
		Response: r.response,
	}
}

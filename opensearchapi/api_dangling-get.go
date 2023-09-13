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

// DanglingGetReq represents possible options for the dangling get request
type DanglingGetReq struct {
	Header http.Header
	Params DanglingGetParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r DanglingGetReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		"/_dangling",
		nil,
		r.Params.get(),
		r.Header,
	)
}

// DanglingGetResp represents the returned struct of the dangling get response
type DanglingGetResp struct {
	Nodes struct {
		Total      int `json:"total"`
		Successful int `json:"successful"`
		Failed     int `json:"failed"`
		Failures   []struct {
			Type     string `json:"type"`
			Reason   string `json:"reason"`
			NodeID   string `json:"node_id"`
			CausedBy struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
			} `json:"caused_by"`
		} `json:"failures"`
	} `json:"_nodes"`
	ClusterName     string `json:"cluster_name"`
	DanglingIndices []struct {
		IndexName          string   `json:"index_name"`
		IndexUUID          string   `json:"index_uuid"`
		CreationDateMillis int64    `json:"creation_date_millis"`
		NodeIds            []string `json:"node_ids"`
	} `json:"dangling_indices"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r DanglingGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

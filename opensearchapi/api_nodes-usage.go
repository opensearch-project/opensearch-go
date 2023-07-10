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

// NodesUsageReq represents possible options for the /_nodes request
type NodesUsageReq struct {
	Metrics []string
	NodeID  []string

	Header http.Header
	Params NodesUsageParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r NodesUsageReq) GetRequest() (*http.Request, error) {
	nodes := strings.Join(r.NodeID, ",")
	metrics := strings.Join(r.Metrics, ",")

	var path strings.Builder

	path.Grow(len("/_nodes//usage/") + len(nodes) + len(metrics))

	path.WriteString("/_nodes")
	if len(r.NodeID) > 0 {
		path.WriteString("/")
		path.WriteString(nodes)
	}
	path.WriteString("/usage")
	if len(r.Metrics) > 0 {
		path.WriteString("/")
		path.WriteString(metrics)
	}

	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// NodesUsageResp represents the returned struct of the /_nodes response
type NodesUsageResp struct {
	NodesUsage struct {
		Total      int `json:"total"`
		Successful int `json:"successful"`
		Failed     int `json:"failed"`
	} `json:"_nodes"`
	ClusterName string                `json:"cluster_name"`
	Nodes       map[string]NodesUsage `json:"nodes"`
	response    *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r NodesUsageResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// NodesUsage is a sub type of NodesUsageResp containing stats about rest api actions
type NodesUsage struct {
	Timestamp    int64           `json:"timestamp"`
	Since        int64           `json:"since"`
	RestActions  map[string]int  `json:"rest_actions"`
	Aggregations json.RawMessage `json:"aggregations"` // Can contain unknow fields
}

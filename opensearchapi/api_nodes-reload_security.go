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
	"io"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
)

// NodesReloadSecurityReq represents possible options for the /_nodes request
type NodesReloadSecurityReq struct {
	NodeID []string

	Body io.Reader

	Header http.Header
	Params NodesReloadSecurityParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r NodesReloadSecurityReq) GetRequest() (*http.Request, error) {
	nodes := strings.Join(r.NodeID, ",")

	var path strings.Builder

	path.Grow(len("/_nodes//reload_secure_settings") + len(nodes))

	path.WriteString("/_nodes")
	if len(r.NodeID) > 0 {
		path.WriteString("/")
		path.WriteString(nodes)
	}
	path.WriteString("/reload_secure_settings")

	return opensearch.BuildRequest(
		"POST",
		path.String(),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// NodesReloadSecurityResp represents the returned struct of the /_nodes response
type NodesReloadSecurityResp struct {
	NodesInfo struct {
		Total      int `json:"total"`
		Successful int `json:"successful"`
		Failed     int `json:"failed"`
	} `json:"_nodes"`
	ClusterName string `json:"cluster_name"`
	Nodes       map[string]struct {
		Name string `json:"name"`
	} `json:"nodes"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r NodesReloadSecurityResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

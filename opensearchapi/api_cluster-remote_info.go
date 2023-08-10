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

// ClusterRemoteInfoReq represents possible options for the /_remote/info request
type ClusterRemoteInfoReq struct {
	Header http.Header
	Params ClusterRemoteInfoParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ClusterRemoteInfoReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		"/_remote/info",
		nil,
		r.Params.get(),
		r.Header,
	)
}

// ClusterRemoteInfoResp represents the returned struct of the ClusterRemoteInfoReq response
type ClusterRemoteInfoResp struct {
	Clusters map[string]ClusterRemoteInfoDetails
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r ClusterRemoteInfoResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// ClusterRemoteInfoDetails is a sub type of ClusterRemoteInfoResp contains information about a remote connection
type ClusterRemoteInfoDetails struct {
	Connected                bool     `json:"connected"`
	Mode                     string   `json:"mode"`
	Seeds                    []string `json:"seeds"`
	NumNodesConnected        int      `json:"num_nodes_connected"`
	MaxConnectionsPerCluster int      `json:"max_connections_per_cluster"`
	InitialConnectTimeout    string   `json:"initial_connect_timeout"`
	SkipUnavailable          bool     `json:"skip_unavailable"`
}

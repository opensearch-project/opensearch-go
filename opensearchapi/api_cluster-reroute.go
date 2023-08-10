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
	"io"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v2"
)

// ClusterRerouteReq represents possible options for the /_cluster/reroute request
type ClusterRerouteReq struct {
	Body io.Reader

	Header http.Header
	Params ClusterRerouteParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ClusterRerouteReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"POST",
		"/_cluster/reroute",
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// ClusterRerouteResp represents the returned struct of the ClusterRerouteReq response
type ClusterRerouteResp struct {
	Acknowledged bool                `json:"acknowledged"`
	State        ClusterRerouteState `json:"state"`
	response     *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r ClusterRerouteResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// ClusterRerouteState is a sub type of ClusterRerouteResp containing information about the cluster and cluster routing
type ClusterRerouteState struct {
	ClusterUUID        string                       `json:"cluster_uuid"`
	Version            int                          `json:"version"`
	StateUUID          string                       `json:"state_uuid"`
	MasterNode         string                       `json:"master_node"`
	ClusterManagerNode string                       `json:"cluster_manager_node"`
	Blocks             json.RawMessage              `json:"blocks"`
	Nodes              map[string]ClusterStateNodes `json:"nodes"`
	RoutingTable       struct {
		Indices map[string]struct {
			Shards map[string][]ClusterStateRoutingIndex `json:"shards"`
		} `json:"indices"`
	} `json:"routing_table"`
	RoutingNodes      ClusterStateRoutingNodes `json:"routing_nodes"`
	RepositoryCleanup struct {
		RepositoryCleanup []json.RawMessage `json:"repository_cleanup"`
	} `json:"repository_cleanup"`
	SnapshotDeletions struct {
		SnapshotDeletions []json.RawMessage `json:"snapshot_deletions"`
	} `json:"snapshot_deletions"`
	Snapshots struct {
		Snapshots []json.RawMessage `json:"snapshots"`
	} `json:"snapshots"`
	Restore struct {
		Snapshots []json.RawMessage `json:"snapshots"`
	} `json:"restore"`
}

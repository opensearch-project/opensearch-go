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
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
)

// SearchShards executes a /_search request with the optional SearchShardsReq
func (c Client) SearchShards(ctx context.Context, req *SearchShardsReq) (*SearchShardsResp, error) {
	if req == nil {
		req = &SearchShardsReq{}
	}

	var (
		data SearchShardsResp
		err  error
	)
	if data.response, err = c.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// SearchShardsReq represents possible options for the /_search request
type SearchShardsReq struct {
	Indices []string

	Header http.Header
	Params SearchShardsParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r SearchShardsReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")
	var path strings.Builder
	path.Grow(len("//_search_shards") + len(indices))
	if len(r.Indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}
	path.WriteString("/_search_shards")

	return opensearch.BuildRequest(
		"POST",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// SearchShardsResp represents the returned struct of the /_search response
type SearchShardsResp struct {
	Nodes map[string]struct {
		Name             string            `json:"name"`
		EphemeralID      string            `json:"ephemeral_id"`
		TransportAddress string            `json:"transport_address"`
		Attributes       map[string]string `json:"attributes"`
	} `json:"nodes"`
	Indices map[string]json.RawMessage `json:"indices"`
	Shards  [][]struct {
		State                    string  `json:"state"`
		Primary                  bool    `json:"primary"`
		Node                     string  `json:"node"`
		RelocatingNode           *string `json:"relocating_node"`
		Shard                    int     `json:"shard"`
		Index                    string  `json:"index"`
		ExpectedShardSizeInBytes int     `json:"expected_shard_size_in_bytes"`
		RecoverySource           struct {
			Type string `json:"type"`
		} `json:"recovery_source"`
		UnassignedInfo struct {
			Reason           string `json:"reason"`
			At               string `json:"at"`
			Delayed          bool   `json:"delayed"`
			AllocationStatus string `json:"allocation_status"`
		} `json:"unassigned_info"`
		AllocationID struct {
			ID string `json:"id"`
		} `json:"allocation_id"`
	} `json:"shards"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r SearchShardsResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

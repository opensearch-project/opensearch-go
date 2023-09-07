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

// IndicesShardStoresReq represents possible options for the index shrink request
type IndicesShardStoresReq struct {
	Indices []string

	Header http.Header
	Params IndicesShardStoresParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IndicesShardStoresReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")

	var path strings.Builder
	path.Grow(15 + len(indices))
	if len(indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}
	path.WriteString("/_shard_stores")
	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// IndicesShardStoresResp represents the returned struct of the index shrink response
type IndicesShardStoresResp struct {
	Indices map[string]struct {
		Shards map[string]struct {
			Stores []json.RawMessage `json:"stores"`
		} `json:"shards"`
	} `json:"indices"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r IndicesShardStoresResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

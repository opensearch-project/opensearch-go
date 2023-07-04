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

// IndicesBlockReq represents possible options for the index create request
type IndicesBlockReq struct {
	Indices []string
	Block   string

	Header http.Header
	Params IndicesBlockParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IndicesBlockReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")

	var path strings.Builder
	path.Grow(9 + len(indices) + len(r.Block))
	path.WriteString("/")
	path.WriteString(indices)
	path.WriteString("/_block/")
	path.WriteString(r.Block)
	return opensearch.BuildRequest(
		"PUT",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// IndicesBlockResp represents the returned struct of the index create response
type IndicesBlockResp struct {
	Acknowledged       bool `json:"acknowledged"`
	ShardsAcknowledged bool `json:"shards_acknowledged"`
	Indices            []struct {
		Name    string `json:"name"`
		Blocked bool   `json:"blocked"`
	} `json:"indices"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r IndicesBlockResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

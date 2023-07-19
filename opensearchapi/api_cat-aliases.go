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

// CatAliasesReq represent possible options for the /_cat/aliases request
type CatAliasesReq struct {
	Aliases []string
	Header  http.Header
	Params  CatAliasesParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r CatAliasesReq) GetRequest() (*http.Request, error) {
	aliases := strings.Join(r.Aliases, ",")
	var path strings.Builder
	path.Grow(len("/_cat/aliases/") + len(aliases))
	path.WriteString("/_cat/aliases")
	if len(r.Aliases) > 0 {
		path.WriteString("/")
		path.WriteString(aliases)
	}
	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// CatAliasesResp represents the returned struct of the /_cat/aliases response
type CatAliasesResp struct {
	Aliases  []CatAliasResp
	response *opensearch.Response
}

// CatAliasResp represents one index of the CatAliasesResp
type CatAliasResp struct {
	Alias         string `json:"alias"`
	Index         string `json:"index"`
	Filter        string `json:"filter"`
	RoutingIndex  string `json:"routing.index"`
	RoutingSearch string `json:"routing.search"`
	IsWriteIndex  string `json:"is_write_index"`
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r CatAliasesResp) Inspect() Inspect {
	return Inspect{
		Response: r.response,
	}
}

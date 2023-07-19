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

// CatNodeAttrsReq represent possible options for the /_cat/nodeattrs request
type CatNodeAttrsReq struct {
	Header http.Header
	Params CatNodeAttrsParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r CatNodeAttrsReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		"/_cat/nodeattrs",
		nil,
		r.Params.get(),
		r.Header,
	)
}

// CatNodeAttrsResp represents the returned struct of the /_cat/nodeattrs response
type CatNodeAttrsResp struct {
	NodeAttrs []CatNodeAttrsItemResp
	response  *opensearch.Response
}

// CatNodeAttrsItemResp represents one index of the CatNodeAttrsResp
type CatNodeAttrsItemResp struct {
	Node  string `json:"node"`
	ID    string `json:"id"`
	PID   int    `json:"pid,string"`
	Host  string `json:"host"`
	IP    string `json:"ip"`
	Port  int    `json:"port,string"`
	Attr  string `json:"attr"`
	Value string `json:"value"`
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r CatNodeAttrsResp) Inspect() Inspect {
	return Inspect{
		Response: r.response,
	}
}

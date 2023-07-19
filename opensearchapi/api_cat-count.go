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

// CatCountReq represent possible options for the /_cat/count request
type CatCountReq struct {
	Indices []string
	Header  http.Header
	Params  CatCountParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r CatCountReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")
	var path strings.Builder
	path.Grow(len("/_cat/count/") + len(indices))
	path.WriteString("/_cat/count")
	if len(r.Indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}
	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// CatCountsResp represents the returned struct of the /_cat/count response
type CatCountsResp struct {
	Counts   []CatCountResp
	response *opensearch.Response
}

// CatCountResp represents one index of the CatCountResp
type CatCountResp struct {
	Epoch     int    `json:"epoch,string"`
	Timestamp string `json:"timestamp"`
	Count     int    `json:"count,string"`
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r CatCountsResp) Inspect() Inspect {
	return Inspect{
		Response: r.response,
	}
}

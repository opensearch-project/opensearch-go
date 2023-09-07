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

// IndicesRefreshReq represents possible options for the <index>/_refresh request
type IndicesRefreshReq struct {
	Indices []string

	Header http.Header
	Params IndicesRefreshParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IndicesRefreshReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")

	var path strings.Builder
	path.Grow(len("//_refresh") + len(indices))
	if len(indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}
	path.WriteString("/_refresh")
	return opensearch.BuildRequest(
		"POST",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// IndicesRefreshResp represents the returned struct of the index shrink response
type IndicesRefreshResp struct {
	Shards struct {
		Total      int `json:"total"`
		Successful int `json:"successful"`
		Failed     int `json:"failed"`
	} `json:"_shards"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r IndicesRefreshResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

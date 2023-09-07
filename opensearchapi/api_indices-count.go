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

// IndicesCountReq represents possible options for the index shrink request
type IndicesCountReq struct {
	Indices []string

	Body io.Reader

	Header http.Header
	Params IndicesCountParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IndicesCountReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")

	var path strings.Builder
	path.Grow(8 + len(indices))
	if len(indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}
	path.WriteString("/_count")
	return opensearch.BuildRequest(
		"POST",
		path.String(),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// IndicesCountResp represents the returned struct of the index shrink response
type IndicesCountResp struct {
	Shards struct {
		Total      int `json:"total"`
		Successful int `json:"successful"`
		Skipped    int `json:"skipped"`
		Failed     int `json:"failed"`
	} `json:"_shards"`
	Count    int `json:"count"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r IndicesCountResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

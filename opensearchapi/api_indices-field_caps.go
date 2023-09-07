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

// IndicesFieldCapsReq represents possible options for the index shrink request
type IndicesFieldCapsReq struct {
	Indices []string

	Body io.Reader

	Header http.Header
	Params IndicesFieldCapsParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IndicesFieldCapsReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")

	var path strings.Builder
	path.Grow(10 + len(indices))
	if len(indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}
	path.WriteString("/_field_caps")
	return opensearch.BuildRequest(
		"POST",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// IndicesFieldCapsResp represents the returned struct of the index shrink response
type IndicesFieldCapsResp struct {
	Indices []string `json:"indices"`
	Fields  map[string]map[string]struct {
		Type         string   `json:"type"`
		Searchable   bool     `json:"searchable"`
		Aggregatable bool     `json:"aggregatable"`
		Indices      []string `json:"indices"`
	} `json:"fields"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r IndicesFieldCapsResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

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
	"fmt"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
)

// IndicesResolveReq represents possible options for the get indices request
type IndicesResolveReq struct {
	Indices []string

	Header http.Header
	Params IndicesResolveParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IndicesResolveReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		fmt.Sprintf("/_resolve/index/%s", strings.Join(r.Indices, ",")),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// IndicesResolveResp represents the returned struct of the get indices response
type IndicesResolveResp struct {
	Indices []struct {
		Name       string   `json:"name"`
		Attributes []string `json:"attributes"`
		Aliases    []string `json:"aliases"`
	} `json:"indices"`
	Aliases []struct {
		Name    string   `json:"name"`
		Indices []string `json:"indices"`
	} `json:"aliases"`
	DataStreams []struct {
		Name           string   `json:"name"`
		BackingIndices []string `json:"backing_indices"`
		TimestampField string   `json:"timestamp_field"`
	} `json:"data_streams"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r IndicesResolveResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

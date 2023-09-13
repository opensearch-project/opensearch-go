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

// ScriptContextReq represents possible options for the delete script request
type ScriptContextReq struct {
	Header http.Header
	Params ScriptContextParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ScriptContextReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		"/_script_context",
		nil,
		r.Params.get(),
		r.Header,
	)
}

// ScriptContextResp represents the returned struct of the delete script response
type ScriptContextResp struct {
	Contexts []struct {
		Name    string `json:"name"`
		Methods []struct {
			Name       string `json:"name"`
			ReturnType string `json:"return_type"`
			Params     []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"params"`
		} `json:"methods"`
	} `json:"contexts"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r ScriptContextResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

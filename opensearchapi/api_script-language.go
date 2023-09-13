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

// ScriptLanguageReq represents possible options for the delete script request
type ScriptLanguageReq struct {
	Header http.Header
	Params ScriptLanguageParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ScriptLanguageReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		"/_script_language",
		nil,
		r.Params.get(),
		r.Header,
	)
}

// ScriptLanguageResp represents the returned struct of the delete script response
type ScriptLanguageResp struct {
	TypesAllowed     []string `json:"types_allowed"`
	LanguageContexts []struct {
		Language string   `json:"language"`
		Contexts []string `json:"contexts"`
	} `json:"language_contexts"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r ScriptLanguageResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

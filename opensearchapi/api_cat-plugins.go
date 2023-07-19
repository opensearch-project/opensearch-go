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

// CatPluginsReq represent possible options for the /_cat/plugins request
type CatPluginsReq struct {
	Header http.Header
	Params CatPluginsParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r CatPluginsReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		"/_cat/plugins",
		nil,
		r.Params.get(),
		r.Header,
	)
}

// CatPluginsResp represents the returned struct of the /_cat/plugins response
type CatPluginsResp struct {
	Plugins  []CatPluginResp
	response *opensearch.Response
}

// CatPluginResp represents one index of the CatPluginsResp
type CatPluginResp struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Component   string `json:"component,omitempty"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r CatPluginsResp) Inspect() Inspect {
	return Inspect{
		Response: r.response,
	}
}

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

// CatTemplatesReq represent possible options for the /_cat/templates request
type CatTemplatesReq struct {
	Templates []string
	Header    http.Header
	Params    CatTemplatesParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r CatTemplatesReq) GetRequest() (*http.Request, error) {
	templates := strings.Join(r.Templates, ",")
	var path strings.Builder
	path.Grow(len("/_cat/templates/") + len(templates))
	path.WriteString("/_cat/templates")
	if len(r.Templates) > 0 {
		path.WriteString("/")
		path.WriteString(templates)
	}
	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// CatTemplatesResp represents the returned struct of the /_cat/templates response
type CatTemplatesResp struct {
	Templates []CatTemplateResp
	response  *opensearch.Response
}

// CatTemplateResp represents one index of the CatTemplatesResp
type CatTemplateResp struct {
	Name          string  `json:"name"`
	IndexPatterns string  `json:"index_patterns"`
	Order         int     `json:"order,string"`
	Version       *string `json:"version"`
	ComposedOf    string  `json:"composed_of"`
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r CatTemplatesResp) Inspect() Inspect {
	return Inspect{
		Response: r.response,
	}
}

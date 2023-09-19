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
	"encoding/json"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
)

// ComponentTemplateGetReq represents possible options for the _component_template get request
type ComponentTemplateGetReq struct {
	ComponentTemplate string

	Header http.Header
	Params ComponentTemplateGetParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ComponentTemplateGetReq) GetRequest() (*http.Request, error) {
	var path strings.Builder
	path.Grow(len("/_component_template/") + len(r.ComponentTemplate))
	path.WriteString("/_component_template")
	if len(r.ComponentTemplate) > 0 {
		path.WriteString("/")
		path.WriteString(r.ComponentTemplate)
	}

	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// ComponentTemplateGetResp represents the returned struct of the index create response
type ComponentTemplateGetResp struct {
	ComponentTemplates []ComponentTemplateGetDetails `json:"component_templates"`
	response           *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r ComponentTemplateGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// ComponentTemplateGetDetails is a sub type of ComponentTemplateGetResp containing information about component template
type ComponentTemplateGetDetails struct {
	Name              string `json:"name"`
	ComponentTemplate struct {
		Template struct {
			Mappings json.RawMessage `json:"mappings"`
			Settings json.RawMessage `json:"settings"`
			Aliases  json.RawMessage `json:"aliases"`
		} `json:"template"`
	} `json:"component_template"`
}

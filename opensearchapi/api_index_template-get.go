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
	"fmt"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
)

// IndexTemplateGetReq represents possible options for the index create request
type IndexTemplateGetReq struct {
	IndexTemplates []string

	Header http.Header
	Params IndexTemplateGetParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IndexTemplateGetReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		fmt.Sprintf("/_index_template/%s", strings.Join(r.IndexTemplates, ",")),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// IndexTemplateGetResp represents the returned struct of the index create response
type IndexTemplateGetResp struct {
	IndexTemplates []IndexTemplateGetDetails `json:"index_templates"`
	response       *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r IndexTemplateGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// IndexTemplateGetDetails is a sub type of IndexTemplateGetResp containing information about an index template
type IndexTemplateGetDetails struct {
	Name          string `json:"name"`
	IndexTemplate struct {
		IndexPatterns []string `json:"index_patterns"`
		Template      struct {
			Mappings json.RawMessage `json:"mappings"`
			Settings json.RawMessage `json:"settings"`
			Aliases  json.RawMessage `json:"aliases"`
		} `json:"template"`
		ComposedOf []string        `json:"composed_of"`
		Priority   int             `json:"priority"`
		Version    int             `json:"version"`
		DataStream json.RawMessage `json:"data_stream"`
	} `json:"index_template"`
}

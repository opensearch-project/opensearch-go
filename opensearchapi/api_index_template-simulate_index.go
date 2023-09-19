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
	"io"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v2"
)

// IndexTemplateSimulateIndexReq represents possible options for the index create request
type IndexTemplateSimulateIndexReq struct {
	Index string

	Body io.Reader

	Header http.Header
	Params IndexTemplateSimulateIndexParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IndexTemplateSimulateIndexReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"POST",
		fmt.Sprintf("/_index_template/_simulate_index/%s", r.Index),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// IndexTemplateSimulateIndexResp represents the returned struct of the index create response
type IndexTemplateSimulateIndexResp struct {
	Template struct {
		Mappings json.RawMessage `json:"mappings"`
		Settings json.RawMessage `json:"settings"`
		Aliases  json.RawMessage `json:"aliases"`
	} `json:"template"`
	Overlapping []struct {
		Name          string   `json:"name"`
		IndexPatterns []string `json:"index_patterns"`
	} `json:"overlapping"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r IndexTemplateSimulateIndexResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

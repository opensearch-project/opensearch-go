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

// IngestGetReq represents possible options for the index create request
type IngestGetReq struct {
	PipelineIDs []string

	Header http.Header
	Params IngestGetParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IngestGetReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		fmt.Sprintf("/_ingest/pipeline/%s", strings.Join(r.PipelineIDs, ",")),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// IngestGetResp represents the returned struct of the index create response
type IngestGetResp struct {
	Pipelines map[string]struct {
		Description string                       `json:"description"`
		Processors  []map[string]json.RawMessage `json:"processors"`
	}
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r IngestGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

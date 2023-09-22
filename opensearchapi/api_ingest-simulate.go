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
	"io"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
)

// IngestSimulateReq represents possible options for the index create request
type IngestSimulateReq struct {
	PipelineID string

	Body io.Reader

	Header http.Header
	Params IngestSimulateParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IngestSimulateReq) GetRequest() (*http.Request, error) {
	var path strings.Builder
	path.Grow(len("/_ingest/pipeline//_simulate") + len(r.PipelineID))
	path.WriteString("/_ingest/pipeline/")
	if len(r.PipelineID) > 0 {
		path.WriteString(r.PipelineID)
		path.WriteString("/")
	}
	path.WriteString("_simulate")
	return opensearch.BuildRequest(
		"POST",
		path.String(),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// IngestSimulateResp represents the returned struct of the index create response
type IngestSimulateResp struct {
	Docs     []json.RawMessage `json:"docs"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r IngestSimulateResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

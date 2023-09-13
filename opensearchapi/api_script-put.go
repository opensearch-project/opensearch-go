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
	"io"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
)

// ScriptPutReq represents possible options for the put script request
type ScriptPutReq struct {
	ScriptID      string
	ScriptContext string

	Body io.Reader

	Header http.Header
	Params ScriptPutParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ScriptPutReq) GetRequest() (*http.Request, error) {
	var path strings.Builder
	path.Grow(len("/_scripts//") + len(r.ScriptID) + len(r.ScriptContext))
	path.WriteString("/_scripts/")
	path.WriteString(r.ScriptID)
	if r.ScriptContext != "" {
		path.WriteString("/")
		path.WriteString(r.ScriptContext)
	}

	return opensearch.BuildRequest(
		"PUT",
		path.String(),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// ScriptPutResp represents the returned struct of the put script response
type ScriptPutResp struct {
	Acknowledged bool `json:"acknowledged"`
	response     *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r ScriptPutResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

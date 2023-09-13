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
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
)

// DocumentDeleteByQueryReq represents possible options for the /<index>/_delete_by_query request
type DocumentDeleteByQueryReq struct {
	Indices []string

	Body io.Reader

	Header http.Header
	Params DocumentDeleteByQueryParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r DocumentDeleteByQueryReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"POST",
		fmt.Sprintf("/%s/_delete_by_query", strings.Join(r.Indices, ",")),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// DocumentDeleteByQueryResp represents the returned struct of the /<index>/_delete_by_query response
type DocumentDeleteByQueryResp struct {
	Took             int  `json:"took"`
	TimedOut         bool `json:"timed_out"`
	Total            int  `json:"total"`
	Deleted          int  `json:"deleted"`
	Batches          int  `json:"batches"`
	VersionConflicts int  `json:"version_conflicts"`
	Noops            int  `json:"noops"`
	Retries          struct {
		Bulk   int `json:"bulk"`
		Search int `json:"search"`
	} `json:"retries"`
	ThrottledMillis      int               `json:"throttled_millis"`
	RequestsPerSecond    float32           `json:"requests_per_second"`
	ThrottledUntilMillis int               `json:"throttled_until_millis"`
	Failures             []json.RawMessage `json:"failures"`       // Unknow struct, open an issue with an example response so we can add it
	Task                 string            `json:"task,omitempty"` // Needed when wait_for_completion is set to false
	response             *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r DocumentDeleteByQueryResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

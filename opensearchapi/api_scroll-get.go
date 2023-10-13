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
	"fmt"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
)

// ScrollGetReq represents possible options for the index create request
type ScrollGetReq struct {
	ScrollID string

	Header http.Header
	Params ScrollGetParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ScrollGetReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"POST",
		"/_search/scroll",
		strings.NewReader(fmt.Sprintf(`{"scroll_id":"%s"}`, r.ScrollID)),
		r.Params.get(),
		r.Header,
	)
}

// ScrollGetResp represents the returned struct of the index create response
type ScrollGetResp struct {
	Took    int            `json:"took"`
	Timeout bool           `json:"timed_out"`
	Shards  ResponseShards `json:"_shards"`
	Hits    struct {
		Total struct {
			Value    int    `json:"value"`
			Relation string `json:"relation"`
		} `json:"total"`
		MaxScore float32     `json:"max_score"`
		Hits     []SearchHit `json:"hits"`
	} `json:"hits"`
	ScrollID        *string `json:"_scroll_id,omitempty"`
	TerminatedEarly bool    `json:"terminated_early"`
	response        *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r ScrollGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

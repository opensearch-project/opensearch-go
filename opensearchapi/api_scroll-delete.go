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

// ScrollDeleteReq represents possible options for the index create request
type ScrollDeleteReq struct {
	ScrollIDs []string

	Body io.Reader

	Header http.Header
	Params ScrollDeleteParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ScrollDeleteReq) GetRequest() (*http.Request, error) {
	scrolls := strings.Join(r.ScrollIDs, ",")
	var path strings.Builder
	path.Grow(len("/_search/scroll/") + len(scrolls))
	path.WriteString("/_search/scroll")
	if len(r.ScrollIDs) > 0 {
		path.WriteString("/")
		path.WriteString(scrolls)
	}
	return opensearch.BuildRequest(
		"DELETE",
		path.String(),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// ScrollDeleteResp represents the returned struct of the index create response
type ScrollDeleteResp struct {
	NumFreed  int  `json:"num_freed"`
	Succeeded bool `json:"succeeded"`
	response  *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r ScrollDeleteResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

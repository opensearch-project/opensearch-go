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
	"io"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v2"
)

// DocumentExplainReq represents possible options for the /<Index>/_explain/<DocumentID> request
type DocumentExplainReq struct {
	Index      string
	DocumentID string

	Body io.Reader

	Header http.Header
	Params DocumentExplainParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r DocumentExplainReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"POST",
		fmt.Sprintf("/%s/_explain/%s", r.Index, r.DocumentID),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// DocumentExplainResp represents the returned struct of the /<Index>/_explain/<DocumentID> response
type DocumentExplainResp struct {
	Index       string                 `json:"_index"`
	ID          string                 `json:"_id"`
	Type        string                 `json:"_type"` // Deprecated field
	Matched     bool                   `json:"matched"`
	Explanation DocumentExplainDetails `json:"explanation"`
	response    *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r DocumentExplainResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// DocumentExplainDetails is a sub type of DocumentExplainResp containing information about why a query does what it does
type DocumentExplainDetails struct {
	Value       float64                  `json:"value"`
	Description string                   `json:"description"`
	Details     []DocumentExplainDetails `json:"details"`
}

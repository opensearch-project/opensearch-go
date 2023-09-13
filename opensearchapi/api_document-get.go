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

	"github.com/opensearch-project/opensearch-go/v2"
)

// DocumentGetReq represents possible options for the /<Index>/_doc/<DocumentID> get request
type DocumentGetReq struct {
	Index      string
	DocumentID string

	Header http.Header
	Params DocumentGetParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r DocumentGetReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		fmt.Sprintf("/%s/_doc/%s", r.Index, r.DocumentID),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// DocumentGetResp represents the returned struct of the /<Index>/_doc/<DocumentID> get response
type DocumentGetResp struct {
	Index       string          `json:"_index"`
	ID          string          `json:"_id"`
	Version     int             `json:"_version"`
	SeqNo       int             `json:"_seq_no"`
	PrimaryTerm int             `json:"_primary_term"`
	Found       bool            `json:"found"`
	Type        string          `json:"_type"` // Deprecated field
	Source      json.RawMessage `json:"_source"`
	response    *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r DocumentGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

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

// DocumentCreateReq represents possible options for the /<index>/_create request
type DocumentCreateReq struct {
	Index      string
	DocumentID string

	Body io.Reader

	Header http.Header
	Params DocumentCreateParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r DocumentCreateReq) GetRequest() (*http.Request, error) {
	var path strings.Builder
	path.Grow(10 + len(r.Index) + len(r.DocumentID))
	path.WriteString("/")
	path.WriteString(r.Index)
	path.WriteString("/_create/")
	path.WriteString(r.DocumentID)
	return opensearch.BuildRequest(
		"PUT",
		path.String(),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// DocumentCreateResp represents the returned struct of the /_doc response
type DocumentCreateResp struct {
	Index         string `json:"_index"`
	ID            string `json:"_id"`
	Version       int    `json:"_version"`
	Result        string `json:"result"`
	Type          string `json:"_type"` // Deprecated field
	ForcedRefresh bool   `json:"forced_refresh"`
	Shards        struct {
		Total      int `json:"total"`
		Successful int `json:"successful"`
		Failed     int `json:"failed"`
	} `json:"_shards"`
	SeqNo       int `json:"_seq_no"`
	PrimaryTerm int `json:"_primary_term"`
	response    *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r DocumentCreateResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

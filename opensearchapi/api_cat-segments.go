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
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
)

// CatSegmentsReq represent possible options for the /_cat/segments request
type CatSegmentsReq struct {
	Indices []string
	Header  http.Header
	Params  CatSegmentsParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r CatSegmentsReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")
	var path strings.Builder
	path.Grow(len("/_cat/segments/") + len(indices))
	path.WriteString("/_cat/segments")
	if len(r.Indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}
	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// CatSegmentsResp represents the returned struct of the /_cat/segments response
type CatSegmentsResp struct {
	Segments []CatSegmentResp
	response *opensearch.Response
}

// CatSegmentResp represents one index of the CatSegmentsResp
type CatSegmentResp struct {
	Index       string `json:"index"`
	Shard       int    `json:"shard,string"`
	Prirep      string `json:"prirep"`
	IP          string `json:"ip"`
	ID          string `json:"id"`
	Segment     string `json:"segment"`
	Generation  int    `json:"generation,string"`
	DocsCount   int    `json:"docs.count,string"`
	DocsDeleted int    `json:"docs.deleted,string"`
	Size        string `json:"size"`
	SizeMemory  string `json:"size.memory"`
	Committed   bool   `json:"committed,string"`
	Searchable  bool   `json:"searchable,string"`
	Version     string `json:"version"`
	Compound    bool   `json:"compound,string"`
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r CatSegmentsResp) Inspect() Inspect {
	return Inspect{
		Response: r.response,
	}
}

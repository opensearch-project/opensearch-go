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

// IndicesSegmentsReq represents possible options for the index shrink request
type IndicesSegmentsReq struct {
	Indices []string

	Header http.Header
	Params IndicesSegmentsParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IndicesSegmentsReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")

	var path strings.Builder
	path.Grow(11 + len(indices))
	if len(indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}
	path.WriteString("/_segments")
	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// IndicesSegmentsResp represents the returned struct of the index shrink response
type IndicesSegmentsResp struct {
	Shards struct {
		Total      int `json:"total"`
		Successful int `json:"successful"`
		Failed     int `json:"failed"`
	} `json:"_shards"`
	Indices map[string]struct {
		Shards map[string][]IndicesSegmentsShards `json:"shards"`
	} `json:"indices"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r IndicesSegmentsResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// IndicesSegmentsShards is a sub type of IndicesSegmentsResp containing information about a shard
type IndicesSegmentsShards struct {
	Routing struct {
		State   string `json:"state"`
		Primary bool   `json:"primary"`
		Node    string `json:"node"`
	} `json:"routing"`
	NumCommittedSegments int                               `json:"num_committed_segments"`
	NumSearchSegments    int                               `json:"num_search_segments"`
	Segments             map[string]IndicesSegmentsDetails `json:"segments"`
}

// IndicesSegmentsDetails is a sub type of IndicesSegmentsShards containing information about a segment
type IndicesSegmentsDetails struct {
	Generation    int    `json:"generation"`
	NumDocs       int    `json:"num_docs"`
	DeletedDocs   int    `json:"deleted_docs"`
	SizeInBytes   int64  `json:"size_in_bytes"`
	MemoryInBytes int    `json:"memory_in_bytes"`
	Committed     bool   `json:"committed"`
	Search        bool   `json:"search"`
	Version       string `json:"version"`
	Compound      bool   `json:"compound"`
	Sort          []struct {
		Field   string `json:"field"`
		Mode    string `json:"mode"`
		Missing string `json:"missing"`
		Reverse bool   `json:"reverse"`
	} `json:"sort"`
	Attributes map[string]string `json:"attributes"`
}

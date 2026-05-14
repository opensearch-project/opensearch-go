// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mlcommons

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// TasksSearchReq represents the search ML tasks request.
//
// Body accepts an OpenSearch query DSL document. When empty, an empty JSON object is
// sent on the wire — OpenSearch 3.x rejects empty bodies with a 400 on _search routes.
//
// Reference: https://docs.opensearch.org/latest/ml-commons-plugin/api/tasks-apis/search-task/
type TasksSearchReq struct {
	Body json.RawMessage

	Params TasksSearchParams
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client.
//
// When Body is empty, an empty JSON object is sent because OpenSearch 3.x rejects the
// _search endpoint with a 400 "request body or source parameter is required" otherwise.
func (r TasksSearchReq) GetRequest() (*http.Request, error) {
	body := r.Body
	if len(body) == 0 {
		body = json.RawMessage(`{}`)
	}

	return opensearch.BuildRequest(
		http.MethodPost,
		"/_plugins/_ml/tasks/_search",
		bytes.NewReader(body),
		r.Params.get(),
		r.Header,
	)
}

// TasksSearchResp wraps the task search response.
type TasksSearchResp struct {
	Took     int  `json:"took,omitempty"`
	TimedOut bool `json:"timed_out,omitempty"`
	Shards   *struct {
		Total      int `json:"total"`
		Successful int `json:"successful"`
		Skipped    int `json:"skipped"`
		Failed     int `json:"failed"`
	} `json:"_shards,omitempty"`
	Hits     *TasksSearchHits `json:"hits,omitempty"`
	response *opensearch.Response
}

// TasksSearchHits is the hits envelope.
type TasksSearchHits struct {
	Total *struct {
		Value    int    `json:"value"`
		Relation string `json:"relation"`
	} `json:"total,omitempty"`
	MaxScore *float64         `json:"max_score,omitempty"`
	Hits     []TasksSearchHit `json:"hits,omitempty"`
}

// TasksSearchHit is a single hit; Source carries the task document.
type TasksSearchHit struct {
	Index  string          `json:"_index,omitempty"`
	ID     string          `json:"_id,omitempty"`
	Score  *float64        `json:"_score,omitempty"`
	Source json.RawMessage `json:"_source,omitempty"`
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r TasksSearchResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

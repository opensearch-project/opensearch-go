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

// ModelsSearchReq represents the search models request.
//
// Body accepts an arbitrary OpenSearch query DSL document. When empty, all models are returned.
//
// Reference: https://docs.opensearch.org/latest/ml-commons-plugin/api/model-apis/search-model/
type ModelsSearchReq struct {
	Body json.RawMessage

	Params ModelsSearchParams
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client.
//
// When Body is empty, an empty JSON object is sent because OpenSearch 3.x rejects the
// _search endpoint with a 400 "request body or source parameter is required" otherwise.
func (r ModelsSearchReq) GetRequest() (*http.Request, error) {
	body := r.Body
	if len(body) == 0 {
		body = json.RawMessage(`{}`)
	}

	return opensearch.BuildRequest(
		http.MethodPost,
		"/_plugins/_ml/models/_search",
		bytes.NewReader(body),
		r.Params.get(),
		r.Header,
	)
}

// ModelsSearchResp wraps the search response. The full body is parsed into Hits / aggregations as JSON
// to remain compatible with arbitrary search queries.
type ModelsSearchResp struct {
	Took     int  `json:"took,omitempty"`
	TimedOut bool `json:"timed_out,omitempty"`
	Shards   *struct {
		Total      int `json:"total"`
		Successful int `json:"successful"`
		Skipped    int `json:"skipped"`
		Failed     int `json:"failed"`
	} `json:"_shards,omitempty"`
	Hits         *ModelsSearchHits `json:"hits,omitempty"`
	Aggregations json.RawMessage   `json:"aggregations,omitempty"`
	response     *opensearch.Response
}

// ModelsSearchHits represents the hits section of a search response.
type ModelsSearchHits struct {
	Total *struct {
		Value    int    `json:"value"`
		Relation string `json:"relation"`
	} `json:"total,omitempty"`
	MaxScore *float64          `json:"max_score,omitempty"`
	Hits     []ModelsSearchHit `json:"hits,omitempty"`
}

// ModelsSearchHit represents a single hit; Source is left raw so model schema variants
// (TEXT_EMBEDDING, REMOTE, SPARSE_ENCODING, …) decode at the caller's discretion.
type ModelsSearchHit struct {
	Index  string          `json:"_index,omitempty"`
	ID     string          `json:"_id,omitempty"`
	Score  *float64        `json:"_score,omitempty"`
	Source json.RawMessage `json:"_source,omitempty"`
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r ModelsSearchResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

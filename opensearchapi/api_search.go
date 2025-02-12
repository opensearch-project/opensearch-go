// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// Search executes a /_search request with the optional SearchReq
func (c Client) Search(ctx context.Context, req *SearchReq) (*SearchResp, *opensearch.Response, error) {
	if req == nil {
		req = &SearchReq{}
	}

	var data SearchResp

	resp, err := c.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// SearchReq represents possible options for the /_search request
type SearchReq struct {
	Indices []string
	Body    io.Reader

	Header http.Header
	Params SearchParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r SearchReq) GetRequest() (*http.Request, error) {
	var path string
	if len(r.Indices) > 0 {
		path = fmt.Sprintf("/%s/_search", strings.Join(r.Indices, ","))
	} else {
		path = "/_search"
	}

	return opensearch.BuildRequest(
		"POST",
		path,
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// SearchResp represents the returned struct of the /_search response
type SearchResp struct {
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
	Errors       bool                 `json:"errors"`
	Aggregations json.RawMessage      `json:"aggregations"`
	ScrollID     *string              `json:"_scroll_id,omitempty"`
	Suggest      map[string][]Suggest `json:"suggest,omitempty"`
}

// SearchHit is a sub type of SearchResp containing information of the search hit with an unparsed Source field
type SearchHit struct {
	Index          string                  `json:"_index"`
	ID             string                  `json:"_id"`
	Routing        string                  `json:"_routing"`
	Score          float32                 `json:"_score"`
	Source         json.RawMessage         `json:"_source"`
	Fields         json.RawMessage         `json:"fields"`
	Type           string                  `json:"_type"` // Deprecated field
	Sort           []any                   `json:"sort"`
	Explanation    *DocumentExplainDetails `json:"_explanation"`
	SeqNo          *int                    `json:"_seq_no"`
	PrimaryTerm    *int                    `json:"_primary_term"`
	Highlight      map[string][]string     `json:"highlight"`
	MatchedQueries []string                `json:"matched_queries"`
}

// Suggest is a sub type of SearchResp containing information of the suggest field
type Suggest struct {
	Text    string `json:"text"`
	Offset  int    `json:"offset"`
	Length  int    `json:"length"`
	Options []struct {
		Text         string  `json:"text"`
		Score        float32 `json:"score"`
		Freq         int     `json:"freq"`
		Highlighted  string  `json:"highlighted"`
		CollateMatch bool    `json:"collate_match"`
	} `json:"options"`
}

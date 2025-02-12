// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// MGet executes a /_mget request with the optional MGetReq
func (c Client) MGet(ctx context.Context, req MGetReq) (*MGetResp, *opensearch.Response, error) {
	var data MGetResp

	resp, err := c.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// MGetReq represents possible options for the /_mget request
type MGetReq struct {
	Index string

	Body io.Reader

	Header http.Header
	Params MGetParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r MGetReq) GetRequest() (*http.Request, error) {
	var path strings.Builder
	path.Grow(len("//_mget") + len(r.Index))
	if len(r.Index) > 0 {
		path.WriteString("/")
		path.WriteString(r.Index)
	}
	path.WriteString("/_mget")
	return opensearch.BuildRequest(
		"POST",
		path.String(),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// MGetResp represents the returned struct of the /_mget response
type MGetResp struct {
	Docs []struct {
		Index       string          `json:"_index"`
		ID          string          `json:"_id"`
		Version     int             `json:"_version"`
		SeqNo       int             `json:"_seq_no"`
		PrimaryTerm int             `json:"_primary_term"`
		Found       bool            `json:"found"`
		Type        string          `json:"_type"`
		Source      json.RawMessage `json:"_source"`
	} `json:"docs"`
}

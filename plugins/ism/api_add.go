// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ism

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// Add executes a add policy request with the required AddReq
func (c Client) Add(ctx context.Context, req AddReq) (AddResp, *opensearch.Response, error) {
	var data AddResp

	resp, err := c.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// AddReq represents possible options for the add policy request
type AddReq struct {
	Indices []string
	Body    AddBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r AddReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	indices := strings.Join(r.Indices, ",")
	var path strings.Builder
	path.Grow(len("/_plugins/_ism/add/") + len(indices))
	path.WriteString("/_plugins/_ism/add")
	if len(r.Indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}

	return opensearch.BuildRequest(
		http.MethodPost,
		path.String(),
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// AddResp represents the returned struct of the add policy response
type AddResp struct {
	UpdatedIndices int           `json:"updated_indices"`
	Failures       bool          `json:"failures"`
	FailedIndices  []FailedIndex `json:"failed_indices"`
}

// AddBody represents the request body for the add policy request
type AddBody struct {
	PolicyID string `json:"policy_id"`
}

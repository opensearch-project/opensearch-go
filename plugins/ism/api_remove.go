// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ism

import (
	"context"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// Remove executes a remove policy request with the required RemoveReq
func (c Client) Remove(ctx context.Context, req RemoveReq) (RemoveResp, *opensearch.Response, error) {
	var data RemoveResp

	resp, err := c.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// RemoveReq represents possible options for the remove policy request
type RemoveReq struct {
	Indices []string

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r RemoveReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")
	var path strings.Builder
	path.Grow(len("/_plugins/_ism/remove/") + len(indices))
	path.WriteString("/_plugins/_ism/remove")
	if len(r.Indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}

	return opensearch.BuildRequest(
		http.MethodPost,
		path.String(),
		nil,
		make(map[string]string),
		r.Header,
	)
}

// RemoveResp represents the returned struct of the remove policy response
type RemoveResp struct {
	UpdatedIndices int           `json:"updated_indices"`
	Failures       bool          `json:"failures"`
	FailedIndices  []FailedIndex `json:"failed_indices"`
}

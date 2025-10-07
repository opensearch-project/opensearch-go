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

// Change executes a change policy request with the required ChangeReq
func (c Client) Change(ctx context.Context, req ChangeReq) (ChangeResp, error) {
	var (
		data ChangeResp
		err  error
	)
	if data.response, err = c.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// ChangeReq represents possible options for the change policy request
type ChangeReq struct {
	Indices []string
	Body    ChangeBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ChangeReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	indices := strings.Join(r.Indices, ",")
	var path strings.Builder
	path.Grow(len("/_plugins/_ism/change_policy/") + len(indices))
	path.WriteString("/_plugins/_ism/change_policy")
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

// ChangeResp represents the returned struct of the change policy response
type ChangeResp struct {
	UpdatedIndices int           `json:"updated_indices"`
	Failures       bool          `json:"failures"`
	FailedIndices  []FailedIndex `json:"failed_indices"`
	response       *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r ChangeResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// ChangeBody represents the request body for the change policy request
type ChangeBody struct {
	PolicyID string              `json:"policy_id"`
	State    string              `json:"state"`
	Include  []ChangeBodyInclude `json:"include,omitempty"`
}

// ChangeBodyInclude is a sub type of ChangeBody containing the state information
type ChangeBodyInclude struct {
	State string `json:"state"`
}

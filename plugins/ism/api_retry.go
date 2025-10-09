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
	"io"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// Retry executes a retry policy request with the required RetryReq
func (c Client) Retry(ctx context.Context, req RetryReq) (RetryResp, error) {
	var (
		data RetryResp
		err  error
	)
	if data.response, err = c.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// RetryReq represents possible options for the retry policy request
type RetryReq struct {
	Indices []string
	Body    *RetryBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r RetryReq) GetRequest() (*http.Request, error) {
	var reqBody io.Reader
	if r.Body != nil {
		body, err := json.Marshal(r.Body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(body)
	}

	indices := strings.Join(r.Indices, ",")
	var path strings.Builder
	path.Grow(len("/_plugins/_ism/retry/") + len(indices))
	path.WriteString("/_plugins/_ism/retry")
	if len(r.Indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}

	return opensearch.BuildRequest(
		http.MethodPost,
		path.String(),
		reqBody,
		make(map[string]string),
		r.Header,
	)
}

// RetryResp represents the returned struct of the retry policy response
type RetryResp struct {
	UpdatedIndices int           `json:"updated_indices"`
	Failures       bool          `json:"failures"`
	FailedIndices  []FailedIndex `json:"failed_indices"`
	response       *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r RetryResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// RetryBody represents the request body for the retry policy request
type RetryBody struct {
	State string `json:"state"`
}

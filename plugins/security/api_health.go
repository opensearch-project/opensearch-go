// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"context"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// Health executes a get health request with the optional HealthReq
func (c Client) Health(ctx context.Context, req *HealthReq) (HealthResp, *opensearch.Response, error) {
	if req == nil {
		req = &HealthReq{}
	}

	var data HealthResp

	resp, err := c.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// HealthReq represents possible options for the health get request
type HealthReq struct {
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r HealthReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		"/_plugins/_security/health",
		nil,
		make(map[string]string),
		r.Header,
	)
}

// HealthResp represents the returned struct of the health get response
type HealthResp struct {
	Message *string `json:"message"`
	Mode    string  `json:"mode"`
	Status  string  `json:"status"`
}

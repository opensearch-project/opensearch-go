// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"

	"github.com/opensearch-project/opensearch-go/v4"
)

type scrollClient struct {
	apiClient *Client
}

// Delete executes a delete scroll request with the required ScrollDeleteReq
func (c scrollClient) Delete(ctx context.Context, req ScrollDeleteReq) (*ScrollDeleteResp, *opensearch.Response, error) {
	var data ScrollDeleteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Get executes a get scroll request with the required ScrollGetReq
func (c scrollClient) Get(ctx context.Context, req ScrollGetReq) (*ScrollGetResp, *opensearch.Response, error) {
	var data ScrollGetResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

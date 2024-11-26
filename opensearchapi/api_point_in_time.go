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

type pointInTimeClient struct {
	apiClient *Client
}

// Create executes a creade pointInTime request with the required PointInTimeCreateReq
func (c pointInTimeClient) Create(ctx context.Context, req PointInTimeCreateReq) (*PointInTimeCreateResp, *opensearch.Response, error) {
	var data PointInTimeCreateResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Delete executes a delete pointInTime request with the required PointInTimeDeleteReq
func (c pointInTimeClient) Delete(ctx context.Context, req PointInTimeDeleteReq) (*PointInTimeDeleteResp, *opensearch.Response, error) {
	var data PointInTimeDeleteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Get executes a get pointInTime request with the optional PointInTimeGetReq
func (c pointInTimeClient) Get(ctx context.Context, req *PointInTimeGetReq) (*PointInTimeGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &PointInTimeGetReq{}
	}

	var data PointInTimeGetResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

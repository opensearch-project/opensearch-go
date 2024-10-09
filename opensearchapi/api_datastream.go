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

type dataStreamClient struct {
	apiClient *Client
}

// Create executes a creade dataStream request with the required DataStreamCreateReq
func (c dataStreamClient) Create(ctx context.Context, req DataStreamCreateReq) (*DataStreamCreateResp, *opensearch.Response, error) {
	var data DataStreamCreateResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Delete executes a delete dataStream request with the required DataStreamDeleteReq
func (c dataStreamClient) Delete(ctx context.Context, req DataStreamDeleteReq) (*DataStreamDeleteResp, *opensearch.Response, error) {
	var data DataStreamDeleteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Get executes a get dataStream request with the optional DataStreamGetReq
func (c dataStreamClient) Get(ctx context.Context, req *DataStreamGetReq) (*DataStreamGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &DataStreamGetReq{}
	}

	var data DataStreamGetResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Stats executes a stats dataStream request with the optional DataStreamStatsReq
func (c dataStreamClient) Stats(ctx context.Context, req *DataStreamStatsReq) (*DataStreamStatsResp, *opensearch.Response, error) {
	if req == nil {
		req = &DataStreamStatsReq{}
	}

	var data DataStreamStatsResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

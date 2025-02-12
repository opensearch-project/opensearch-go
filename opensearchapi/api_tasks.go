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

type tasksClient struct {
	apiClient *Client
}

// Cancel executes a delete tasks request with the required TasksCancelReq
func (c tasksClient) Cancel(ctx context.Context, req TasksCancelReq) (*TasksCancelResp, *opensearch.Response, error) {
	var data TasksCancelResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// List executes a get tasks request with the optional TasksListReq
func (c tasksClient) List(ctx context.Context, req *TasksListReq) (*TasksListResp, *opensearch.Response, error) {
	if req == nil {
		req = &TasksListReq{}
	}

	var data TasksListResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Get executes a get tasks request with the optional TasksGetReq
func (c tasksClient) Get(ctx context.Context, req TasksGetReq) (*TasksGetResp, *opensearch.Response, error) {
	var data TasksGetResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

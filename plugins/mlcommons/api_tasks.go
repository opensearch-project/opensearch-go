// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mlcommons

import "context"

type tasksClient struct {
	apiClient *Client
}

// Get executes a get task request with the required TasksGetReq
func (c tasksClient) Get(ctx context.Context, req TasksGetReq) (TasksGetResp, error) {
	var (
		data TasksGetResp
		err  error
	)
	if data.response, err = do(ctx, c.apiClient, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Delete executes a delete task request with the required TasksDeleteReq
func (c tasksClient) Delete(ctx context.Context, req TasksDeleteReq) (TasksDeleteResp, error) {
	var (
		data TasksDeleteResp
		err  error
	)
	if data.response, err = do(ctx, c.apiClient, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Search executes a search tasks request with the optional TasksSearchReq
func (c tasksClient) Search(ctx context.Context, req *TasksSearchReq) (TasksSearchResp, error) {
	if req == nil {
		req = &TasksSearchReq{}
	}

	var (
		data TasksSearchResp
		err  error
	)
	if data.response, err = do(ctx, c.apiClient, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

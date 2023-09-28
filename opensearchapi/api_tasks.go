// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package opensearchapi

import (
	"context"
)

type tasksClient struct {
	apiClient *Client
}

// Cancel executes a delete tasks request with the required TasksCancelReq
func (c tasksClient) Cancel(ctx context.Context, req TasksCancelReq) (*TasksCancelResp, error) {
	var (
		data TasksCancelResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// List executes a get tasks request with the optional TasksListReq
func (c tasksClient) List(ctx context.Context, req *TasksListReq) (*TasksListResp, error) {
	if req == nil {
		req = &TasksListReq{}
	}

	var (
		data TasksListResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Get executes a get tasks request with the optional TasksGetReq
func (c tasksClient) Get(ctx context.Context, req TasksGetReq) (*TasksGetResp, error) {
	var (
		data TasksGetResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

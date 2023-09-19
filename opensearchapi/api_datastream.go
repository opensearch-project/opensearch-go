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

type dataStreamClient struct {
	apiClient *Client
}

// Create executes a creade dataStream request with the required DataStreamCreateReq
func (c dataStreamClient) Create(ctx context.Context, req DataStreamCreateReq) (*DataStreamCreateResp, error) {
	var (
		data DataStreamCreateResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Delete executes a delete dataStream request with the required DataStreamDeleteReq
func (c dataStreamClient) Delete(ctx context.Context, req DataStreamDeleteReq) (*DataStreamDeleteResp, error) {
	var (
		data DataStreamDeleteResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Get executes a get dataStream request with the optional DataStreamGetReq
func (c dataStreamClient) Get(ctx context.Context, req *DataStreamGetReq) (*DataStreamGetResp, error) {
	if req == nil {
		req = &DataStreamGetReq{}
	}

	var (
		data DataStreamGetResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Stats executes a stats dataStream request with the optional DataStreamStatsReq
func (c dataStreamClient) Stats(ctx context.Context, req *DataStreamStatsReq) (*DataStreamStatsResp, error) {
	if req == nil {
		req = &DataStreamStatsReq{}
	}

	var (
		data DataStreamStatsResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

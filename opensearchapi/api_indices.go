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

	"github.com/opensearch-project/opensearch-go/v2"
)

type indicesClient struct {
	apiClient *Client
}

// Delete executes a delete indices request with the required IndicesDeleteReq
func (c indicesClient) Delete(ctx context.Context, req IndicesDeleteReq) (*IndicesDeleteResp, error) {
	var (
		data IndicesDeleteResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Create executes a creade indices request with the required IndicesCreateReq
func (c indicesClient) Create(ctx context.Context, req IndicesCreateReq) (*IndicesCreateResp, error) {
	var (
		data IndicesCreateResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Exists executes a exists indices request with the required IndicesExistsReq
func (c indicesClient) Exists(ctx context.Context, req IndicesExistsReq) (*opensearch.Response, error) {
	resp, err := c.apiClient.do(ctx, req, nil)
	if err != nil {
		return resp, err
	}

	return resp, nil
}

// Block executes a /<index>/_block request with the required IndicesBlockReq
func (c indicesClient) Block(ctx context.Context, req IndicesBlockReq) (*IndicesBlockResp, error) {
	var (
		data IndicesBlockResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Analyze executes a /<index>/_analyze request with the required IndicesAnalyzeReq
func (c indicesClient) Analyze(ctx context.Context, req IndicesAnalyzeReq) (*IndicesAnalyzeResp, error) {
	var (
		data IndicesAnalyzeResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// ClearCache executes a /<index>/_cache/clear request with the optional IndicesClearCacheReq
func (c indicesClient) ClearCache(ctx context.Context, req *IndicesClearCacheReq) (*IndicesClearCacheResp, error) {
	if req == nil {
		req = &IndicesClearCacheReq{}
	}

	var (
		data IndicesClearCacheResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

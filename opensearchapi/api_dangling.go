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

type danglingClient struct {
	apiClient *Client
}

// Delete executes a delete dangling request with the required DanglingDeleteReq
func (c danglingClient) Delete(ctx context.Context, req DanglingDeleteReq) (*DanglingDeleteResp, *opensearch.Response, error) {
	var data DanglingDeleteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Import executes an import dangling request with the required DanglingImportReq
func (c danglingClient) Import(ctx context.Context, req DanglingImportReq) (*DanglingImportResp, *opensearch.Response, error) {
	var data DanglingImportResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Get executes a /_dangling request with the optional DanglingGetReq
func (c danglingClient) Get(ctx context.Context, req *DanglingGetReq) (*DanglingGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &DanglingGetReq{}
	}

	var data DanglingGetResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

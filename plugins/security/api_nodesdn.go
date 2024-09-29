// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"context"
	"github.com/opensearch-project/opensearch-go/v4"
)

type nodesdnClient struct {
	apiClient *Client
}

// Get executes a get nodesdn request with the optional NodesDNGetReq
func (c nodesdnClient) Get(ctx context.Context, req *NodesDNGetReq) (NodesDNGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &NodesDNGetReq{}
	}

	var data NodesDNGetResp

	resp, err := c.apiClient.do(ctx, req, &data.DistinguishedNames)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Put executes a put nodesdn request with the required NodesDNPutReq
func (c nodesdnClient) Put(ctx context.Context, req NodesDNPutReq) (NodesDNPutResp, *opensearch.Response, error) {
	var data NodesDNPutResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Delete executes a delete nodesdn request with the required NodesDNDeleteReq
func (c nodesdnClient) Delete(ctx context.Context, req NodesDNDeleteReq) (NodesDNDeleteResp, *opensearch.Response, error) {
	var data NodesDNDeleteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Patch executes a put nodesdn request with the required NodesDNPatchReq
func (c nodesdnClient) Patch(ctx context.Context, req NodesDNPatchReq) (NodesDNPatchResp, *opensearch.Response, error) {
	var data NodesDNPatchResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

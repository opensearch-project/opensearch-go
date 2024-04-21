// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"context"
)

type nodesdnClient struct {
	apiClient *Client
}

// Get executes a get nodesdn request with the optional NodesDNGetReq
func (c nodesdnClient) Get(ctx context.Context, req *NodesDNGetReq) (NodesDNGetResp, error) {
	if req == nil {
		req = &NodesDNGetReq{}
	}

	var (
		data NodesDNGetResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data.DistinguishedNames); err != nil {
		return data, err
	}

	return data, nil
}

// Put executes a put nodesdn request with the required NodesDNPutReq
func (c nodesdnClient) Put(ctx context.Context, req NodesDNPutReq) (NodesDNPutResp, error) {
	var (
		data NodesDNPutResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Delete executes a delete nodesdn request with the required NodesDNDeleteReq
func (c nodesdnClient) Delete(ctx context.Context, req NodesDNDeleteReq) (NodesDNDeleteResp, error) {
	var (
		data NodesDNDeleteResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Patch executes a put nodesdn request with the required NodesDNPatchReq
func (c nodesdnClient) Patch(ctx context.Context, req NodesDNPatchReq) (NodesDNPatchResp, error) {
	var (
		data NodesDNPatchResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

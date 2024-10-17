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

type rolesmappingClient struct {
	apiClient *Client
}

// Get executes a get roles request with the optional RolesMappingGetReq
func (c rolesmappingClient) Get(ctx context.Context, req *RolesMappingGetReq) (RolesMappingGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &RolesMappingGetReq{}
	}

	var data RolesMappingGetResp

	resp, err := c.apiClient.do(ctx, req, &data.RolesMapping)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Put executes a put roles request with the required RolesMappingPutReq
func (c rolesmappingClient) Put(ctx context.Context, req RolesMappingPutReq) (RolesMappingPutResp, *opensearch.Response, error) {
	var data RolesMappingPutResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Delete executes a delete roles request with the required RolesMappingDeleteReq
func (c rolesmappingClient) Delete(ctx context.Context, req RolesMappingDeleteReq) (RolesMappingDeleteResp, *opensearch.Response, error) {
	var data RolesMappingDeleteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Patch executes a patch roles request with the required RolesMappingPatchReq
func (c rolesmappingClient) Patch(ctx context.Context, req RolesMappingPatchReq) (RolesMappingPatchResp, *opensearch.Response, error) {
	var data RolesMappingPatchResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

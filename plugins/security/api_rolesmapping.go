// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"context"
)

type rolesmappingClient struct {
	apiClient *Client
}

// Get executes a get roles request with the optional RolesMappingGetReq
func (c rolesmappingClient) Get(ctx context.Context, req *RolesMappingGetReq) (RolesMappingGetResp, error) {
	if req == nil {
		req = &RolesMappingGetReq{}
	}

	var (
		data RolesMappingGetResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data.RolesMapping); err != nil {
		return data, err
	}

	return data, nil
}

// Put executes a put roles request with the required RolesMappingPutReq
func (c rolesmappingClient) Put(ctx context.Context, req RolesMappingPutReq) (RolesMappingPutResp, error) {
	var (
		data RolesMappingPutResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Delete executes a delete roles request with the required RolesMappingDeleteReq
func (c rolesmappingClient) Delete(ctx context.Context, req RolesMappingDeleteReq) (RolesMappingDeleteResp, error) {
	var (
		data RolesMappingDeleteResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Patch executes a patch roles request with the required RolesMappingPatchReq
func (c rolesmappingClient) Patch(ctx context.Context, req RolesMappingPatchReq) (RolesMappingPatchResp, error) {
	var (
		data RolesMappingPatchResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

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

type rolesClient struct {
	apiClient *Client
}

// Get executes a get roles request with the optional RolesGetReq
func (c rolesClient) Get(ctx context.Context, req *RolesGetReq) (RolesGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &RolesGetReq{}
	}

	var data RolesGetResp

	resp, err := c.apiClient.do(ctx, req, &data.Roles)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Put executes a put roles request with the required RolesPutReq
func (c rolesClient) Put(ctx context.Context, req RolesPutReq) (RolesPutResp, *opensearch.Response, error) {
	var data RolesPutResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Delete executes a delete roles request with the required RolesDeleteReq
func (c rolesClient) Delete(ctx context.Context, req RolesDeleteReq) (RolesDeleteResp, *opensearch.Response, error) {
	var data RolesDeleteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Patch executes a patch roles request with the required RolesPatchReq
func (c rolesClient) Patch(ctx context.Context, req RolesPatchReq) (RolesPatchResp, *opensearch.Response, error) {
	var data RolesPatchResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

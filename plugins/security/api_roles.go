// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"context"
)

type rolesClient struct {
	apiClient *Client
}

// Get executes a get roles request with the optional RolesGetReq
func (c rolesClient) Get(ctx context.Context, req *RolesGetReq) (RolesGetResp, error) {
	if req == nil {
		req = &RolesGetReq{}
	}

	var (
		data RolesGetResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data.Roles); err != nil {
		return data, err
	}

	return data, nil
}

// Put executes a put roles request with the required RolesPutReq
func (c rolesClient) Put(ctx context.Context, req RolesPutReq) (RolesPutResp, error) {
	var (
		data RolesPutResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Delete executes a delete roles request with the required RolesDeleteReq
func (c rolesClient) Delete(ctx context.Context, req RolesDeleteReq) (RolesDeleteResp, error) {
	var (
		data RolesDeleteResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Patch executes a patch roles request with the required RolesPatchReq
func (c rolesClient) Patch(ctx context.Context, req RolesPatchReq) (RolesPatchResp, error) {
	var (
		data RolesPatchResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// RolesIndexPermission contains index permissions and is used for Get and Put requests
type RolesIndexPermission struct {
	IndexPatterns  []string `json:"index_patterns,omitempty"`
	DLS            string   `json:"dls,omitempty"`
	FLS            []string `json:"fls,omitempty"`
	MaskedFields   []string `json:"masked_fields,omitempty"`
	AllowedActions []string `json:"allowed_actions,omitempty"`
}

// RolesTenantPermission contains tenant permissions and is used for Get and Put requests
type RolesTenantPermission struct {
	TenantPatterns []string `json:"tenant_patterns,omitempty"`
	AllowedActions []string `json:"allowed_actions,omitempty"`
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"context"
)

type actiongroupsClient struct {
	apiClient *Client
}

// Get executes a get actiongroups request with the optional ActionGroupsGetReq
func (c actiongroupsClient) Get(ctx context.Context, req *ActionGroupsGetReq) (ActionGroupsGetResp, error) {
	if req == nil {
		req = &ActionGroupsGetReq{}
	}

	var (
		data ActionGroupsGetResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data.Groups); err != nil {
		return data, err
	}

	return data, nil
}

// Put executes a put actiongroups request with the required ActionGroupsPutReq
func (c actiongroupsClient) Put(ctx context.Context, req ActionGroupsPutReq) (ActionGroupsPutResp, error) {
	var (
		data ActionGroupsPutResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Delete executes a delete actiongroups request with the required ActionGroupsDeleteReq
func (c actiongroupsClient) Delete(ctx context.Context, req ActionGroupsDeleteReq) (ActionGroupsDeleteResp, error) {
	var (
		data ActionGroupsDeleteResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Patch executes a patch actiongroups request with the required ActionGroupsPatchReq
func (c actiongroupsClient) Patch(ctx context.Context, req ActionGroupsPatchReq) (ActionGroupsPatchResp, error) {
	var (
		data ActionGroupsPatchResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

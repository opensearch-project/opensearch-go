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

type actiongroupsClient struct {
	apiClient *Client
}

// Get executes a get actiongroups request with the optional ActionGroupsGetReq
func (c actiongroupsClient) Get(ctx context.Context, req *ActionGroupsGetReq) (ActionGroupsGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &ActionGroupsGetReq{}
	}

	var data ActionGroupsGetResp

	resp, err := c.apiClient.do(ctx, req, &data.Groups)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Put executes a put actiongroups request with the required ActionGroupsPutReq
func (c actiongroupsClient) Put(ctx context.Context, req ActionGroupsPutReq) (ActionGroupsPutResp, *opensearch.Response, error) {
	var data ActionGroupsPutResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Delete executes a delete actiongroups request with the required ActionGroupsDeleteReq
func (c actiongroupsClient) Delete(ctx context.Context, req ActionGroupsDeleteReq) (ActionGroupsDeleteResp, *opensearch.Response, error) {
	var data ActionGroupsDeleteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Patch executes a patch actiongroups request with the required ActionGroupsPatchReq
func (c actiongroupsClient) Patch(ctx context.Context, req ActionGroupsPatchReq) (ActionGroupsPatchResp, *opensearch.Response, error) {
	var data ActionGroupsPatchResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

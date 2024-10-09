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

type internalusersClient struct {
	apiClient *Client
}

// Get executes a get internalusers request with the optional InternalUsersGetReq
func (c internalusersClient) Get(ctx context.Context, req *InternalUsersGetReq) (InternalUsersGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &InternalUsersGetReq{}
	}

	var (
		data InternalUsersGetResp
		err  error
	)

	resp, err := c.apiClient.do(ctx, req, &data.Users)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Put executes a put internalusers request with the required InternalUsersPutReq
func (c internalusersClient) Put(ctx context.Context, req InternalUsersPutReq) (InternalUsersPutResp, *opensearch.Response, error) {
	var (
		data InternalUsersPutResp
		err  error
	)

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Delete executes a delete internalusers request with the required InternalUsersDeleteReq
func (c internalusersClient) Delete(ctx context.Context, req InternalUsersDeleteReq) (InternalUsersDeleteResp, *opensearch.Response, error) {
	var (
		data InternalUsersDeleteResp
		err  error
	)

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Patch executes a patch internalusers request with the required InternalUsersPatchReq
func (c internalusersClient) Patch(ctx context.Context, req InternalUsersPatchReq) (InternalUsersPatchResp, *opensearch.Response, error) {
	var (
		data InternalUsersPatchResp
		err  error
	)

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

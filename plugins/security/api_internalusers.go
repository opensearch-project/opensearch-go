// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"context"
)

type internalusersClient struct {
	apiClient *Client
}

// Get executes a get internalusers request with the optional InternalUsersGetReq
func (c internalusersClient) Get(ctx context.Context, req *InternalUsersGetReq) (InternalUsersGetResp, error) {
	if req == nil {
		req = &InternalUsersGetReq{}
	}

	var (
		data InternalUsersGetResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Put executes a put internalusers request with the required InternalUsersPutReq
func (c internalusersClient) Put(ctx context.Context, req InternalUsersPutReq) (InternalUsersPutResp, error) {
	var (
		data InternalUsersPutResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Delete executes a delete internalusers request with the required InternalUsersDeleteReq
func (c internalusersClient) Delete(ctx context.Context, req InternalUsersDeleteReq) (InternalUsersDeleteResp, error) {
	var (
		data InternalUsersDeleteResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Patch executes a patch internalusers request with the required InternalUsersPatchReq
func (c internalusersClient) Patch(ctx context.Context, req InternalUsersPatchReq) (InternalUsersPatchResp, error) {
	var (
		data InternalUsersPatchResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

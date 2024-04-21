// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"context"
)

type securityconfigClient struct {
	apiClient *Client
}

// Get executes a get securityconfig request with the optional ConfigGetReq
func (c securityconfigClient) Get(ctx context.Context, req *ConfigGetReq) (ConfigGetResp, error) {
	if req == nil {
		req = &ConfigGetReq{}
	}

	var (
		data ConfigGetResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Put executes a put securityconfig request with the required ConfigPutReq
func (c securityconfigClient) Put(ctx context.Context, req ConfigPutReq) (ConfigPutResp, error) {
	var (
		data ConfigPutResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Patch executes a patch securityconfig request with the required ConfigPatchReq
func (c securityconfigClient) Patch(ctx context.Context, req ConfigPatchReq) (ConfigPatchResp, error) {
	var (
		data ConfigPatchResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

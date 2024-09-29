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

type securityconfigClient struct {
	apiClient *Client
}

// Get executes a get securityconfig request with the optional ConfigGetReq
func (c securityconfigClient) Get(ctx context.Context, req *ConfigGetReq) (ConfigGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &ConfigGetReq{}
	}

	var data ConfigGetResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Put executes a put securityconfig request with the required ConfigPutReq
func (c securityconfigClient) Put(ctx context.Context, req ConfigPutReq) (ConfigPutResp, *opensearch.Response, error) {
	var data ConfigPutResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Patch executes a patch securityconfig request with the required ConfigPatchReq
func (c securityconfigClient) Patch(ctx context.Context, req ConfigPatchReq) (ConfigPatchResp, *opensearch.Response, error) {
	var data ConfigPatchResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

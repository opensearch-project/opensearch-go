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

type tenantsClient struct {
	apiClient *Client
}

// Get executes a get tenants request with the optional TenantsGetReq
func (c tenantsClient) Get(ctx context.Context, req *TenantsGetReq) (TenantsGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &TenantsGetReq{}
	}

	var data TenantsGetResp

	resp, err := c.apiClient.do(ctx, req, &data.Tenants)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Put executes a put tenants request with the required TenantsPutReq
func (c tenantsClient) Put(ctx context.Context, req TenantsPutReq) (TenantsPutResp, *opensearch.Response, error) {
	var data TenantsPutResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Delete executes a delete tenants request with the required TenantsDeleteReq
func (c tenantsClient) Delete(ctx context.Context, req TenantsDeleteReq) (TenantsDeleteResp, *opensearch.Response, error) {
	var data TenantsDeleteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Patch executes a patch tenants request with the required TenantsPatchReq
func (c tenantsClient) Patch(ctx context.Context, req TenantsPatchReq) (TenantsPatchResp, *opensearch.Response, error) {
	var data TenantsPatchResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

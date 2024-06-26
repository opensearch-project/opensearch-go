// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"context"
)

type tenantsClient struct {
	apiClient *Client
}

// Get executes a get tenants request with the optional TenantsGetReq
func (c tenantsClient) Get(ctx context.Context, req *TenantsGetReq) (TenantsGetResp, error) {
	if req == nil {
		req = &TenantsGetReq{}
	}

	var (
		data TenantsGetResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data.Tenants); err != nil {
		return data, err
	}

	return data, nil
}

// Put executes a put tenants request with the required TenantsPutReq
func (c tenantsClient) Put(ctx context.Context, req TenantsPutReq) (TenantsPutResp, error) {
	var (
		data TenantsPutResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Delete executes a delete tenants request with the required TenantsDeleteReq
func (c tenantsClient) Delete(ctx context.Context, req TenantsDeleteReq) (TenantsDeleteResp, error) {
	var (
		data TenantsDeleteResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Patch executes a patch tenants request with the required TenantsPatchReq
func (c tenantsClient) Patch(ctx context.Context, req TenantsPatchReq) (TenantsPatchResp, error) {
	var (
		data TenantsPatchResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

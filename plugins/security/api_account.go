// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"context"
)

type accountClient struct {
	apiClient *Client
}

// Get executes a get account request with the optional AccountGetReq
func (c accountClient) Get(ctx context.Context, req *AccountGetReq) (AccountGetResp, error) {
	if req == nil {
		req = &AccountGetReq{}
	}

	var (
		data AccountGetResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Put executes a put account request with the required AccountPutReq
func (c accountClient) Put(ctx context.Context, req AccountPutReq) (AccountPutResp, error) {
	var (
		data AccountPutResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

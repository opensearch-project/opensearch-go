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

type accountClient struct {
	apiClient *Client
}

// Get executes a get account request with the optional AccountGetReq
func (c accountClient) Get(ctx context.Context, req *AccountGetReq) (AccountGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &AccountGetReq{}
	}

	var data AccountGetResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Put executes a put account request with the required AccountPutReq
func (c accountClient) Put(ctx context.Context, req AccountPutReq) (AccountPutResp, *opensearch.Response, error) {
	var data AccountPutResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

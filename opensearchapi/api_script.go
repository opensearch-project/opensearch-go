// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"

	"github.com/opensearch-project/opensearch-go/v4"
)

type scriptClient struct {
	apiClient *Client
}

// Delete executes a delete script request with the required ScriptDeleteReq
func (c scriptClient) Delete(ctx context.Context, req ScriptDeleteReq) (*ScriptDeleteResp, *opensearch.Response, error) {
	var data ScriptDeleteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Put executes an put script request with the required ScriptPutReq
func (c scriptClient) Put(ctx context.Context, req ScriptPutReq) (*ScriptPutResp, *opensearch.Response, error) {
	var data ScriptPutResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Get executes a /_script request with the required ScriptGetReq
func (c scriptClient) Get(ctx context.Context, req ScriptGetReq) (*ScriptGetResp, *opensearch.Response, error) {
	var data ScriptGetResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Context executes a /_script_context request with the optional ScriptContextReq
func (c scriptClient) Context(ctx context.Context, req *ScriptContextReq) (*ScriptContextResp, *opensearch.Response, error) {
	if req == nil {
		req = &ScriptContextReq{}
	}

	var data ScriptContextResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Language executes a /_script_context request with the optional ScriptLanguageReq
func (c scriptClient) Language(ctx context.Context, req *ScriptLanguageReq) (*ScriptLanguageResp, *opensearch.Response, error) {
	if req == nil {
		req = &ScriptLanguageReq{}
	}

	var data ScriptLanguageResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// PainlessExecute executes a /_script request with the required ScriptPainlessExecuteReq
func (c scriptClient) PainlessExecute(
	ctx context.Context,
	req ScriptPainlessExecuteReq,
) (*ScriptPainlessExecuteResp, *opensearch.Response, error) {
	var data ScriptPainlessExecuteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

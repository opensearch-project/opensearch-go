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

type documentClient struct {
	apiClient *Client
}

// Create executes a creade document request with the required DocumentCreateReq
func (c documentClient) Create(ctx context.Context, req DocumentCreateReq) (*DocumentCreateResp, *opensearch.Response, error) {
	var data DocumentCreateResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Delete executes a delete document request with the required DocumentDeleteReq
func (c documentClient) Delete(ctx context.Context, req DocumentDeleteReq) (*DocumentDeleteResp, *opensearch.Response, error) {
	var data DocumentDeleteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// DeleteByQuery executes a delete by query request with the required DocumentDeleteByQueryReq
func (c documentClient) DeleteByQuery(
	ctx context.Context,
	req DocumentDeleteByQueryReq,
) (*DocumentDeleteByQueryResp, *opensearch.Response, error) {
	var data DocumentDeleteByQueryResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// DeleteByQueryRethrottle executes a delete by query rethrottle request with the optional DocumentDeleteByQueryRethrottleReq
func (c documentClient) DeleteByQueryRethrottle(
	ctx context.Context,
	req DocumentDeleteByQueryRethrottleReq,
) (*DocumentDeleteByQueryRethrottleResp, *opensearch.Response, error) {
	var data DocumentDeleteByQueryRethrottleResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Exists executes a exists document request with the required DocumentExistsReq
func (c documentClient) Exists(ctx context.Context, req DocumentExistsReq) (*opensearch.Response, error) {
	return c.apiClient.do(ctx, req, nil)
}

// ExistsSource executes a exists source request with the required DocumentExistsSourceReq
func (c documentClient) ExistsSource(ctx context.Context, req DocumentExistsSourceReq) (*opensearch.Response, error) {
	return c.apiClient.do(ctx, req, nil)
}

// Explain executes an explain document request with the required DocumentExplainReq
func (c documentClient) Explain(ctx context.Context, req DocumentExplainReq) (*DocumentExplainResp, *opensearch.Response, error) {
	var data DocumentExplainResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Get executes a /<Index>/_doc/<DocumentID> request with the required DocumentGetReq
func (c documentClient) Get(ctx context.Context, req DocumentGetReq) (*DocumentGetResp, *opensearch.Response, error) {
	var data DocumentGetResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Source executes a /<Index>/_source/<DocumentID> request with the required DocumentSourceReq
func (c documentClient) Source(ctx context.Context, req DocumentSourceReq) (*DocumentSourceResp, *opensearch.Response, error) {
	var data DocumentSourceResp

	resp, err := c.apiClient.do(ctx, req, &data.Source)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

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

type ingestClient struct {
	apiClient *Client
}

// Create executes a creade ingest request with the required IngestCreateReq
func (c ingestClient) Create(ctx context.Context, req IngestCreateReq) (*IngestCreateResp, *opensearch.Response, error) {
	var data IngestCreateResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Delete executes a delete ingest request with the required IngestDeleteReq
func (c ingestClient) Delete(ctx context.Context, req IngestDeleteReq) (*IngestDeleteResp, *opensearch.Response, error) {
	var data IngestDeleteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Get executes a get ingest request with the optional IngestGetReq
func (c ingestClient) Get(ctx context.Context, req *IngestGetReq) (*IngestGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &IngestGetReq{}
	}

	var data IngestGetResp

	resp, err := c.apiClient.do(ctx, req, &data.Pipelines)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Simulate executes a stats ingest request with the optional IngestSimulateReq
func (c ingestClient) Simulate(ctx context.Context, req IngestSimulateReq) (*IngestSimulateResp, *opensearch.Response, error) {
	var data IngestSimulateResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Grok executes a get ingest request with the optional IngestGrokReq
func (c ingestClient) Grok(ctx context.Context, req *IngestGrokReq) (*IngestGrokResp, *opensearch.Response, error) {
	if req == nil {
		req = &IngestGrokReq{}
	}

	var data IngestGrokResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

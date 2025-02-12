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

type nodesClient struct {
	apiClient *Client
}

// Stats executes a /_nodes/_stats request with the optional NodesStatsReq
func (c nodesClient) Stats(ctx context.Context, req *NodesStatsReq) (*NodesStatsResp, *opensearch.Response, error) {
	if req == nil {
		req = &NodesStatsReq{}
	}

	var data NodesStatsResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Info executes a /_nodes request with the optional NodesInfoReq
func (c nodesClient) Info(ctx context.Context, req *NodesInfoReq) (*NodesInfoResp, *opensearch.Response, error) {
	if req == nil {
		req = &NodesInfoReq{}
	}

	var data NodesInfoResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// HotThreads executes a /_nodes/hot_threads request with the optional NodesHotThreadsReq
func (c nodesClient) HotThreads(ctx context.Context, req *NodesHotThreadsReq) (*opensearch.Response, error) {
	if req == nil {
		req = &NodesHotThreadsReq{}
	}
	return c.apiClient.do(ctx, req, nil)
}

// ReloadSecurity executes a /_nodes/reload_secure_settings request with the optional NodesReloadSecurityReq
func (c nodesClient) ReloadSecurity(
	ctx context.Context,
	req *NodesReloadSecurityReq,
) (*NodesReloadSecurityResp, *opensearch.Response, error) {
	if req == nil {
		req = &NodesReloadSecurityReq{}
	}

	var data NodesReloadSecurityResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Usage executes a /_nodes/usage request with the optional NodesUsageReq
func (c nodesClient) Usage(ctx context.Context, req *NodesUsageReq) (*NodesUsageResp, *opensearch.Response, error) {
	if req == nil {
		req = &NodesUsageReq{}
	}

	var data NodesUsageResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

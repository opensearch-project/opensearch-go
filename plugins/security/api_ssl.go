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

type sslClient struct {
	apiClient *Client
}

// Get executes a get ssl request with the optional SSLGetReq
func (c sslClient) Get(ctx context.Context, req *SSLGetReq) (SSLGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &SSLGetReq{}
	}

	var data SSLGetResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// HTTPReload executes a reload ssl request with the optional SSLHTTPReloadReq
func (c sslClient) HTTPReload(ctx context.Context, req *SSLHTTPReloadReq) (SSLHTTPReloadResp, *opensearch.Response, error) {
	if req == nil {
		req = &SSLHTTPReloadReq{}
	}

	var data SSLHTTPReloadResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// TransportReload executes a reload ssl request with the optional SSLTransportReloadReq
func (c sslClient) TransportReload(ctx context.Context, req *SSLTransportReloadReq) (SSLTransportReloadResp, *opensearch.Response, error) {
	if req == nil {
		req = &SSLTransportReloadReq{}
	}

	var data SSLTransportReloadResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

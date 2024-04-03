// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"context"
)

type sslClient struct {
	apiClient *Client
}

// Get executes a get ssl request with the optional SSLGetReq
func (c sslClient) Get(ctx context.Context, req *SSLGetReq) (SSLGetResp, error) {
	if req == nil {
		req = &SSLGetReq{}
	}

	var (
		data SSLGetResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// HTTPReload executes a reload ssl request with the optional SSLHTTPReloadReq
func (c sslClient) HTTPReload(ctx context.Context, req *SSLHTTPReloadReq) (SSLHTTPReloadResp, error) {
	if req == nil {
		req = &SSLHTTPReloadReq{}
	}

	var (
		data SSLHTTPReloadResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// TransportReload executes a reload ssl request with the optional SSLTransportReloadReq
func (c sslClient) TransportReload(ctx context.Context, req *SSLTransportReloadReq) (SSLTransportReloadResp, error) {
	if req == nil {
		req = &SSLTransportReloadReq{}
	}

	var (
		data SSLTransportReloadResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

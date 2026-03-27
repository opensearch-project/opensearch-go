// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"
	"net/http"
)

type scrollClient struct {
	apiClient *Client
}

// Delete executes a delete scroll request with the required ScrollDeleteReq
func (c scrollClient) Delete(ctx context.Context, req ScrollDeleteReq) (*ScrollDeleteResp, error) {
	var (
		data ScrollDeleteResp
		err  error
	)
	if data.response, err = do(ctx, c.apiClient, http.MethodDelete, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Get executes a get scroll request with the required ScrollGetReq
func (c scrollClient) Get(ctx context.Context, req ScrollGetReq) (*ScrollGetResp, error) {
	var (
		data ScrollGetResp
		err  error
	)
	if data.response, err = do(ctx, c.apiClient, http.MethodPost, req, &data); err != nil {
		return &data, err
	}

	if c.apiClient.returnQueryErrors && data.Shards.Failed > 0 {
		return &data, &PartialSearchError{
			FailedShards: data.Shards.Failed,
			TotalShards:  data.Shards.Total,
			Failures:     data.Shards.Failures,
		}
	}

	return &data, nil
}

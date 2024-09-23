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

type indicesClient struct {
	apiClient *Client
	Alias     aliasClient
	Mapping   mappingClient
	Settings  settingsClient
}

// Delete executes a delete indices request with the required IndicesDeleteReq
func (c indicesClient) Delete(ctx context.Context, req IndicesDeleteReq) (*IndicesDeleteResp, *opensearch.Response, error) {
	var data IndicesDeleteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Create executes a creade indices request with the required IndicesCreateReq
func (c indicesClient) Create(ctx context.Context, req IndicesCreateReq) (*IndicesCreateResp, *opensearch.Response, error) {
	var data IndicesCreateResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Exists executes a exists indices request with the required IndicesExistsReq
func (c indicesClient) Exists(ctx context.Context, req IndicesExistsReq) (*opensearch.Response, error) {
	return c.apiClient.do(ctx, req, nil)
}

// Block executes a /<index>/_block request with the required IndicesBlockReq
func (c indicesClient) Block(ctx context.Context, req IndicesBlockReq) (*IndicesBlockResp, *opensearch.Response, error) {
	var data IndicesBlockResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Analyze executes a /<index>/_analyze request with the required IndicesAnalyzeReq
func (c indicesClient) Analyze(ctx context.Context, req IndicesAnalyzeReq) (*IndicesAnalyzeResp, *opensearch.Response, error) {
	var data IndicesAnalyzeResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// ClearCache executes a /<index>/_cache/clear request with the optional IndicesClearCacheReq
func (c indicesClient) ClearCache(ctx context.Context, req *IndicesClearCacheReq) (*IndicesClearCacheResp, *opensearch.Response, error) {
	if req == nil {
		req = &IndicesClearCacheReq{}
	}

	var data IndicesClearCacheResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Clone executes a /<index>/_clone/<target> request with the required IndicesCloneReq
func (c indicesClient) Clone(ctx context.Context, req IndicesCloneReq) (*IndicesCloneResp, *opensearch.Response, error) {
	var data IndicesCloneResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Close executes a /<index>/_close request with the required IndicesCloseReq
func (c indicesClient) Close(ctx context.Context, req IndicesCloseReq) (*IndicesCloseResp, *opensearch.Response, error) {
	var data IndicesCloseResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Get executes a /<index> request with the required IndicesGetReq
func (c indicesClient) Get(ctx context.Context, req IndicesGetReq) (*IndicesGetResp, *opensearch.Response, error) {
	var data IndicesGetResp

	resp, err := c.apiClient.do(ctx, req, &data.Indices)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Open executes a /<index>/_open request with the required IndicesOpenReq
func (c indicesClient) Open(ctx context.Context, req IndicesOpenReq) (*IndicesOpenResp, *opensearch.Response, error) {
	var data IndicesOpenResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Shrink executes a /<index>/_shrink/<target> request with the required IndicesShrinkReq
func (c indicesClient) Shrink(ctx context.Context, req IndicesShrinkReq) (*IndicesShrinkResp, *opensearch.Response, error) {
	var data IndicesShrinkResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Split executes a /<index>/_split/<target> request with the required IndicesSplitReq
func (c indicesClient) Split(ctx context.Context, req IndicesSplitReq) (*IndicesSplitResp, *opensearch.Response, error) {
	var data IndicesSplitResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Flush executes a /<index>/_flush request with the optional IndicesFlushReq
func (c indicesClient) Flush(ctx context.Context, req *IndicesFlushReq) (*IndicesFlushResp, *opensearch.Response, error) {
	if req == nil {
		req = &IndicesFlushReq{}
	}

	var data IndicesFlushResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Forcemerge executes a /<index>/_forcemerge request with the optional IndicesForcemergeReq
func (c indicesClient) Forcemerge(ctx context.Context, req *IndicesForcemergeReq) (*IndicesForcemergeResp, *opensearch.Response, error) {
	if req == nil {
		req = &IndicesForcemergeReq{}
	}

	var data IndicesForcemergeResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Recovery executes a /<index>/_recovery request with the optional IndicesRecoveryReq
func (c indicesClient) Recovery(ctx context.Context, req *IndicesRecoveryReq) (*IndicesRecoveryResp, *opensearch.Response, error) {
	if req == nil {
		req = &IndicesRecoveryReq{}
	}

	var data IndicesRecoveryResp

	resp, err := c.apiClient.do(ctx, req, &data.Indices)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Refresh executes a /<index>/_refresh request with the optional IndicesRefreshReq
func (c indicesClient) Refresh(ctx context.Context, req *IndicesRefreshReq) (*IndicesRefreshResp, *opensearch.Response, error) {
	if req == nil {
		req = &IndicesRefreshReq{}
	}

	var data IndicesRefreshResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Rollover executes a /<index>/_rollover request with the required IndicesRolloverReq
func (c indicesClient) Rollover(ctx context.Context, req IndicesRolloverReq) (*IndicesRolloverResp, *opensearch.Response, error) {
	var data IndicesRolloverResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Segments executes a /<index>/_segments request with the optional IndicesSegmentsReq
func (c indicesClient) Segments(ctx context.Context, req *IndicesSegmentsReq) (*IndicesSegmentsResp, *opensearch.Response, error) {
	if req == nil {
		req = &IndicesSegmentsReq{}
	}

	var data IndicesSegmentsResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// ShardStores executes a /<index>/_shard_stores request with the optional IndicesShardStoresReq
func (c indicesClient) ShardStores(ctx context.Context, req *IndicesShardStoresReq) (*IndicesShardStoresResp, *opensearch.Response, error) {
	if req == nil {
		req = &IndicesShardStoresReq{}
	}

	var data IndicesShardStoresResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Stats executes a /<index>/_stats request with the optional IndicesStatsReq
func (c indicesClient) Stats(ctx context.Context, req *IndicesStatsReq) (*IndicesStatsResp, *opensearch.Response, error) {
	if req == nil {
		req = &IndicesStatsReq{}
	}

	var data IndicesStatsResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// ValidateQuery executes a /<index>/_validate/query request with the required IndicesValidateQueryReq
func (c indicesClient) ValidateQuery(
	ctx context.Context,
	req IndicesValidateQueryReq,
) (*IndicesValidateQueryResp, *opensearch.Response, error) {
	var data IndicesValidateQueryResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Count executes a /<index>/_count request with the required IndicesCountReq
func (c indicesClient) Count(ctx context.Context, req *IndicesCountReq) (*IndicesCountResp, *opensearch.Response, error) {
	if req == nil {
		req = &IndicesCountReq{}
	}

	var data IndicesCountResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// FieldCaps executes a /<index>/_field_caps request with the required IndicesFieldCapsReq
func (c indicesClient) FieldCaps(ctx context.Context, req IndicesFieldCapsReq) (*IndicesFieldCapsResp, *opensearch.Response, error) {
	var data IndicesFieldCapsResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Resolve executes a /_resolve/index/<indices> request with the required IndicesResolveReq
func (c indicesClient) Resolve(ctx context.Context, req IndicesResolveReq) (*IndicesResolveResp, *opensearch.Response, error) {
	var data IndicesResolveResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

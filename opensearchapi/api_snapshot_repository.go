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

type repositoryClient struct {
	apiClient *Client
}

// Create executes a put repository request with the required SnapshotRepositoryCreateReq
func (c repositoryClient) Create(
	ctx context.Context,
	req SnapshotRepositoryCreateReq,
) (*SnapshotRepositoryCreateResp, *opensearch.Response, error) {
	var data SnapshotRepositoryCreateResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Delete executes a delete repository request with the required SnapshotRepositoryDeleteReq
func (c repositoryClient) Delete(
	ctx context.Context,
	req SnapshotRepositoryDeleteReq,
) (*SnapshotRepositoryDeleteResp, *opensearch.Response, error) {
	var data SnapshotRepositoryDeleteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Get executes a get repository request with the optional SnapshotRepositoryGetReq
func (c repositoryClient) Get(
	ctx context.Context,
	req *SnapshotRepositoryGetReq,
) (*SnapshotRepositoryGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &SnapshotRepositoryGetReq{}
	}
	var data SnapshotRepositoryGetResp

	resp, err := c.apiClient.do(ctx, req, &data.Repos)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Cleanup executes a cleanup repository request with the required SnapshotRepositoryCleanupReq
func (c repositoryClient) Cleanup(
	ctx context.Context,
	req SnapshotRepositoryCleanupReq,
) (*SnapshotRepositoryCleanupResp, *opensearch.Response, error) {
	var data SnapshotRepositoryCleanupResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Verify executes a verify repository request with the required SnapshotRepositoryVerifyReq
func (c repositoryClient) Verify(
	ctx context.Context,
	req SnapshotRepositoryVerifyReq,
) (*SnapshotRepositoryVerifyResp, *opensearch.Response, error) {
	var data SnapshotRepositoryVerifyResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

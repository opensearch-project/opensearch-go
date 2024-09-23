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

type snapshotClient struct {
	apiClient  *Client
	Repository repositoryClient
}

// Create executes a creade snapshot request with the required SnapshotCreateReq
func (c snapshotClient) Create(ctx context.Context, req SnapshotCreateReq) (*SnapshotCreateResp, *opensearch.Response, error) {
	var data SnapshotCreateResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Delete executes a delete snapshot request with the required SnapshotDeleteReq
func (c snapshotClient) Delete(ctx context.Context, req SnapshotDeleteReq) (*SnapshotDeleteResp, *opensearch.Response, error) {
	var data SnapshotDeleteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Get executes a get snapshot request with the required SnapshotGetReq
func (c snapshotClient) Get(ctx context.Context, req SnapshotGetReq) (*SnapshotGetResp, *opensearch.Response, error) {
	var data SnapshotGetResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Clone executes a snapshot clone request with the required SnapshotCloneReq
func (c snapshotClient) Clone(ctx context.Context, req SnapshotCloneReq) (*SnapshotCloneResp, *opensearch.Response, error) {
	var data SnapshotCloneResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Restore executes a snapshot restore request with the required SnapshotRestoreReq
func (c snapshotClient) Restore(ctx context.Context, req SnapshotRestoreReq) (*SnapshotRestoreResp, *opensearch.Response, error) {
	var data SnapshotRestoreResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Status executes a snapshot status request with the required SnapshotStatusReq
func (c snapshotClient) Status(ctx context.Context, req SnapshotStatusReq) (*SnapshotStatusResp, *opensearch.Response, error) {
	var data SnapshotStatusResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

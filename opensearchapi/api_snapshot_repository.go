// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package opensearchapi

import (
	"context"
)

type repositoryClient struct {
	apiClient *Client
}

// Create executes a put repository request with the required SnapshotRepositoryCreateReq
func (c repositoryClient) Create(ctx context.Context, req SnapshotRepositoryCreateReq) (*SnapshotRepositoryCreateResp, error) {
	var (
		data SnapshotRepositoryCreateResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Delete executes a delete repository request with the required SnapshotRepositoryDeleteReq
func (c repositoryClient) Delete(ctx context.Context, req SnapshotRepositoryDeleteReq) (*SnapshotRepositoryDeleteResp, error) {
	var (
		data SnapshotRepositoryDeleteResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Get executes a get repository request with the optional SnapshotRepositoryGetReq
func (c repositoryClient) Get(ctx context.Context, req *SnapshotRepositoryGetReq) (*SnapshotRepositoryGetResp, error) {
	if req == nil {
		req = &SnapshotRepositoryGetReq{}
	}
	var (
		data SnapshotRepositoryGetResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data.Repos); err != nil {
		return &data, err
	}

	return &data, nil
}

// Cleanup executes a cleanup repository request with the required SnapshotRepositoryCleanupReq
func (c repositoryClient) Cleanup(ctx context.Context, req SnapshotRepositoryCleanupReq) (*SnapshotRepositoryCleanupResp, error) {
	var (
		data SnapshotRepositoryCleanupResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Verify executes a verify repository request with the required SnapshotRepositoryVerifyReq
func (c repositoryClient) Verify(ctx context.Context, req SnapshotRepositoryVerifyReq) (*SnapshotRepositoryVerifyResp, error) {
	var (
		data SnapshotRepositoryVerifyResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

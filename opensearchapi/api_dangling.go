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

type danglingClient struct {
	apiClient *Client
}

// Delete executes a delete dangling request with the required DanglingDeleteReq
func (c danglingClient) Delete(ctx context.Context, req DanglingDeleteReq) (*DanglingDeleteResp, error) {
	var (
		data DanglingDeleteResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Import executes an import dangling request with the required DanglingImportReq
func (c danglingClient) Import(ctx context.Context, req DanglingImportReq) (*DanglingImportResp, error) {
	var (
		data DanglingImportResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Get executes a /_dangling request with the optional DanglingGetReq
func (c danglingClient) Get(ctx context.Context, req *DanglingGetReq) (*DanglingGetResp, error) {
	if req == nil {
		req = &DanglingGetReq{}
	}

	var (
		data DanglingGetResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

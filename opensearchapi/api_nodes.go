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

	"github.com/opensearch-project/opensearch-go/v2"
)

type nodesClient struct {
	apiClient *Client
}

// Stats executes a /_nodes/_stats request with the optional NodesStatsReq
func (c nodesClient) Stats(ctx context.Context, req *NodesStatsReq) (*NodesStatsResp, error) {
	if req == nil {
		req = &NodesStatsReq{}
	}

	var (
		data NodesStatsResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Info executes a /_nodes request with the optional NodesInfoReq
func (c nodesClient) Info(ctx context.Context, req *NodesInfoReq) (*NodesInfoResp, error) {
	if req == nil {
		req = &NodesInfoReq{}
	}

	var (
		data NodesInfoResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// HotThreads executes a /_nodes/hot_threads request with the optional NodesHotThreadsReq
func (c nodesClient) HotThreads(ctx context.Context, req *NodesHotThreadsReq) (*opensearch.Response, error) {
	if req == nil {
		req = &NodesHotThreadsReq{}
	}
	return c.apiClient.do(ctx, req, nil)
}

// ReloadSecurity executes a /_nodes/reload_secure_settings request with the optional NodesReloadSecurityReq
func (c nodesClient) ReloadSecurity(ctx context.Context, req *NodesReloadSecurityReq) (*NodesReloadSecurityResp, error) {
	if req == nil {
		req = &NodesReloadSecurityReq{}
	}

	var (
		data NodesReloadSecurityResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Usage executes a /_nodes/usage request with the optional NodesUsageReq
func (c nodesClient) Usage(ctx context.Context, req *NodesUsageReq) (*NodesUsageResp, error) {
	if req == nil {
		req = &NodesUsageReq{}
	}

	var (
		data NodesUsageResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

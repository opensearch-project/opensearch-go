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

type catClient struct {
	apiClient *Client
}

// Aliases executes a /_cat/aliases request with the optional CatAliasesReq
func (c catClient) Aliases(ctx context.Context, req *CatAliasesReq) (*CatAliasesResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatAliasesReq{}
	}

	var data CatAliasesResp

	resp, err := c.apiClient.do(ctx, req, &data.Aliases)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Allocation executes a /_cat/allocation request with the optional CatAllocationReq
func (c catClient) Allocation(ctx context.Context, req *CatAllocationReq) (*CatAllocationsResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatAllocationReq{}
	}

	var data CatAllocationsResp

	resp, err := c.apiClient.do(ctx, req, &data.Allocations)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// ClusterManager executes a /_cat/cluster_manager request with the optional CatClusterManagerReq
func (c catClient) ClusterManager(ctx context.Context, req *CatClusterManagerReq) (*CatClusterManagersResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatClusterManagerReq{}
	}

	var data CatClusterManagersResp

	resp, err := c.apiClient.do(ctx, req, &data.ClusterManagers)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Count executes a /_cat/count request with the optional CatCountReq
func (c catClient) Count(ctx context.Context, req *CatCountReq) (*CatCountsResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatCountReq{}
	}

	var data CatCountsResp

	resp, err := c.apiClient.do(ctx, req, &data.Counts)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// FieldData executes a /_cat/fielddata request with the optional CatFieldDataReq
func (c catClient) FieldData(ctx context.Context, req *CatFieldDataReq) (*CatFieldDataResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatFieldDataReq{}
	}

	var data CatFieldDataResp

	resp, err := c.apiClient.do(ctx, req, &data.FieldData)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Health executes a /_cat/health request with the optional CatHealthReq
func (c catClient) Health(ctx context.Context, req *CatHealthReq) (*CatHealthResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatHealthReq{}
	}

	var data CatHealthResp

	resp, err := c.apiClient.do(ctx, req, &data.Health)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Indices executes a /_cat/indices request with the optional CatIndicesReq
func (c catClient) Indices(ctx context.Context, req *CatIndicesReq) (*CatIndicesResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatIndicesReq{}
	}

	var data CatIndicesResp

	resp, err := c.apiClient.do(ctx, req, &data.Indices)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Master executes a /_cat/master request with the optional CatMasterReq
func (c catClient) Master(ctx context.Context, req *CatMasterReq) (*CatMasterResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatMasterReq{}
	}

	var data CatMasterResp

	resp, err := c.apiClient.do(ctx, req, &data.Master)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// NodeAttrs executes a /_cat/nodeattrs request with the optional CatNodeAttrsReq
func (c catClient) NodeAttrs(ctx context.Context, req *CatNodeAttrsReq) (*CatNodeAttrsResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatNodeAttrsReq{}
	}

	var data CatNodeAttrsResp

	resp, err := c.apiClient.do(ctx, req, &data.NodeAttrs)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Nodes executes a /_cat/nodes request with the optional CatNodesReq
func (c catClient) Nodes(ctx context.Context, req *CatNodesReq) (*CatNodesResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatNodesReq{}
	}

	var data CatNodesResp

	resp, err := c.apiClient.do(ctx, req, &data.Nodes)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// PendingTasks executes a /_cat/pending_tasks request with the optional CatPendingTasksReq
func (c catClient) PendingTasks(ctx context.Context, req *CatPendingTasksReq) (*CatPendingTasksResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatPendingTasksReq{}
	}

	var data CatPendingTasksResp

	resp, err := c.apiClient.do(ctx, req, &data.PendingTasks)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Plugins executes a /_cat/plugins request with the optional CatPluginsReq
func (c catClient) Plugins(ctx context.Context, req *CatPluginsReq) (*CatPluginsResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatPluginsReq{}
	}

	var data CatPluginsResp

	resp, err := c.apiClient.do(ctx, req, &data.Plugins)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Recovery executes a /_cat/recovery request with the optional CatRecoveryReq
func (c catClient) Recovery(ctx context.Context, req *CatRecoveryReq) (*CatRecoveryResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatRecoveryReq{}
	}

	var data CatRecoveryResp

	resp, err := c.apiClient.do(ctx, req, &data.Recovery)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Repositories executes a /_cat/repositories request with the optional CatRepositoriesReq
func (c catClient) Repositories(ctx context.Context, req *CatRepositoriesReq) (*CatRepositoriesResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatRepositoriesReq{}
	}

	var data CatRepositoriesResp

	resp, err := c.apiClient.do(ctx, req, &data.Repositories)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Segments executes a /_cat/segments request with the optional CatSegmentsReq
func (c catClient) Segments(ctx context.Context, req *CatSegmentsReq) (*CatSegmentsResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatSegmentsReq{}
	}

	var data CatSegmentsResp

	resp, err := c.apiClient.do(ctx, req, &data.Segments)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Shards executes a /_cat/shards request with the optional CatShardsReq
func (c catClient) Shards(ctx context.Context, req *CatShardsReq) (*CatShardsResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatShardsReq{}
	}

	var data CatShardsResp

	resp, err := c.apiClient.do(ctx, req, &data.Shards)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Snapshots executes a /_cat/snapshots request with the required CatSnapshotsReq
func (c catClient) Snapshots(ctx context.Context, req CatSnapshotsReq) (*CatSnapshotsResp, *opensearch.Response, error) {
	var data CatSnapshotsResp

	resp, err := c.apiClient.do(ctx, req, &data.Snapshots)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Tasks executes a /_cat/tasks request with the optional CatTasksReq
func (c catClient) Tasks(ctx context.Context, req *CatTasksReq) (*CatTasksResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatTasksReq{}
	}

	var data CatTasksResp

	resp, err := c.apiClient.do(ctx, req, &data.Tasks)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Templates executes a /_cat/templates request with the optional CatTemplatesReq
func (c catClient) Templates(ctx context.Context, req *CatTemplatesReq) (*CatTemplatesResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatTemplatesReq{}
	}

	var data CatTemplatesResp

	resp, err := c.apiClient.do(ctx, req, &data.Templates)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// ThreadPool executes a /_cat/thread_pool request with the optional CatThreadPoolReq
func (c catClient) ThreadPool(ctx context.Context, req *CatThreadPoolReq) (*CatThreadPoolResp, *opensearch.Response, error) {
	if req == nil {
		req = &CatThreadPoolReq{}
	}

	var data CatThreadPoolResp

	resp, err := c.apiClient.do(ctx, req, &data.ThreadPool)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

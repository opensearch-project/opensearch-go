// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mlcommons

import (
	"context"
	"encoding/json"
)

type modelsClient struct {
	apiClient *Client
}

// Register executes a register model request with the required ModelsRegisterReq
func (c modelsClient) Register(ctx context.Context, req ModelsRegisterReq) (ModelsRegisterResp, error) {
	var (
		data ModelsRegisterResp
		err  error
	)
	if data.response, err = do(ctx, c.apiClient, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Get executes a get model request with the required ModelsGetReq
func (c modelsClient) Get(ctx context.Context, req ModelsGetReq) (ModelsGetResp, error) {
	var (
		data ModelsGetResp
		err  error
	)
	if data.response, err = do(ctx, c.apiClient, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Update executes an update model request with the required ModelsUpdateReq
func (c modelsClient) Update(ctx context.Context, req ModelsUpdateReq) (ModelsUpdateResp, error) {
	var (
		data ModelsUpdateResp
		err  error
	)
	if data.response, err = do(ctx, c.apiClient, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Deploy executes a deploy model request with the required ModelsDeployReq
func (c modelsClient) Deploy(ctx context.Context, req ModelsDeployReq) (ModelsDeployResp, error) {
	var (
		data ModelsDeployResp
		err  error
	)
	if data.response, err = do(ctx, c.apiClient, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Undeploy executes an undeploy model request with the required ModelsUndeployReq
func (c modelsClient) Undeploy(ctx context.Context, req ModelsUndeployReq) (ModelsUndeployResp, error) {
	var (
		data ModelsUndeployResp
		err  error
	)
	if data.response, err = do(ctx, c.apiClient, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Delete executes a delete model request with the required ModelsDeleteReq
func (c modelsClient) Delete(ctx context.Context, req ModelsDeleteReq) (ModelsDeleteResp, error) {
	var (
		data ModelsDeleteResp
		err  error
	)
	if data.response, err = do(ctx, c.apiClient, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Search executes a search models request with the optional ModelsSearchReq
func (c modelsClient) Search(ctx context.Context, req *ModelsSearchReq) (ModelsSearchResp, error) {
	if req == nil {
		req = &ModelsSearchReq{}
	}

	var (
		data ModelsSearchResp
		err  error
	)
	if data.response, err = do(ctx, c.apiClient, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// Predict executes a predict request against a deployed model
func (c modelsClient) Predict(ctx context.Context, req ModelsPredictReq) (ModelsPredictResp, error) {
	var (
		data ModelsPredictResp
		err  error
	)
	if data.response, err = do(ctx, c.apiClient, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// UploadChunk uploads a single chunk of a model artifact
func (c modelsClient) UploadChunk(ctx context.Context, req ModelsUploadChunkReq) (ModelsUploadChunkResp, error) {
	var (
		data ModelsUploadChunkResp
		err  error
	)
	if data.response, err = do(ctx, c.apiClient, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// ModelConfig describes a model's inference configuration (commonly used for embedding / TEXT_EMBEDDING models).
// Less common fields are surfaced via Additional as raw JSON to remain forward-compatible with
// future model types without breaking changes.
type ModelConfig struct {
	ModelType            string          `json:"model_type,omitempty"`
	EmbeddingDimension   *int            `json:"embedding_dimension,omitempty"`
	FrameworkType        string          `json:"framework_type,omitempty"`
	AllConfig            string          `json:"all_config,omitempty"`
	PoolingMode          string          `json:"pooling_mode,omitempty"`
	Normalize            *bool           `json:"normalize_result,omitempty"`
	ModelMaxLength       *int            `json:"model_max_length,omitempty"`
	QueryPrefix          string          `json:"query_prefix,omitempty"`
	PassagePrefix        string          `json:"passage_prefix,omitempty"`
	AdditionalProperties json.RawMessage `json:"-"`
}

// ModelTaskInfo is the async write envelope returned by register/deploy/undeploy and similar writes.
// The real status is fetched via Tasks.Get(task_id).
type ModelTaskInfo struct {
	TaskID string `json:"task_id,omitempty"`
	Status string `json:"status,omitempty"`
}

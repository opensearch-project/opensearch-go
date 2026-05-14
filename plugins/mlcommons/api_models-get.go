// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mlcommons

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// ModelsGetReq represents the get model request.
//
// Reference: https://docs.opensearch.org/latest/ml-commons-plugin/api/model-apis/get-model/
type ModelsGetReq struct {
	ModelID string

	Params ModelsGetParams
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ModelsGetReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		http.MethodGet,
		fmt.Sprintf("/_plugins/_ml/models/%s", r.ModelID),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// ModelsGetResp represents the get model response.
//
// The full document is exposed verbatim via Source so consumers can decode model-type-specific fields
// without coupling the client to every concrete model schema.
type ModelsGetResp struct {
	Name                string          `json:"name,omitempty"`
	ModelGroupID        string          `json:"model_group_id,omitempty"`
	Algorithm           string          `json:"algorithm,omitempty"`
	ModelVersion        string          `json:"model_version,omitempty"`
	ModelFormat         string          `json:"model_format,omitempty"`
	ModelState          string          `json:"model_state,omitempty"`
	ModelContentSize    *int64          `json:"model_content_size_in_bytes,omitempty"`
	ModelContentHash    string          `json:"model_content_hash_value,omitempty"`
	ModelConfig         *ModelConfig    `json:"model_config,omitempty"`
	CreatedTime         *int64          `json:"created_time,omitempty"`
	LastUpdatedTime     *int64          `json:"last_updated_time,omitempty"`
	LastRegisteredTime  *int64          `json:"last_registered_time,omitempty"`
	LastDeployedTime    *int64          `json:"last_deployed_time,omitempty"`
	LastUndeployedTime  *int64          `json:"last_undeployed_time,omitempty"`
	TotalChunks         *int            `json:"total_chunks,omitempty"`
	PlanningWorkerNodes []string        `json:"planning_worker_nodes,omitempty"`
	CurrentWorkerNodes  []string        `json:"current_worker_nodes,omitempty"`
	IsHidden            *bool           `json:"is_hidden,omitempty"`
	ConnectorID         string          `json:"connector_id,omitempty"`
	Connector           json.RawMessage `json:"connector,omitempty"`
	Source              json.RawMessage `json:"-"`
	response            *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r ModelsGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mlcommons

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// ModelsRegisterReq represents the register model request.
//
// Reference: https://docs.opensearch.org/latest/ml-commons-plugin/api/model-apis/register-model/
type ModelsRegisterReq struct {
	Body ModelsRegisterBody

	Params ModelsRegisterParams
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ModelsRegisterReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(
		http.MethodPost,
		"/_plugins/_ml/models/_register",
		bytes.NewReader(body),
		r.Params.get(),
		r.Header,
	)
}

// ModelsRegisterBody represents the JSON body for registering a model.
//
// The shape varies by FunctionName. Common fields are exposed directly; less common ones can be supplied via Connector / ConnectorID / Guardrails or by composing the body manually.
type ModelsRegisterBody struct {
	Name                  string          `json:"name"`
	Version               string          `json:"version,omitempty"`
	ModelGroupID          string          `json:"model_group_id,omitempty"`
	Description           string          `json:"description,omitempty"`
	FunctionName          string          `json:"function_name,omitempty"`
	ModelFormat           string          `json:"model_format,omitempty"`
	ModelConfig           *ModelConfig    `json:"model_config,omitempty"`
	URL                   string          `json:"url,omitempty"`
	ModelContentHashValue string          `json:"model_content_hash_value,omitempty"`
	ConnectorID           string          `json:"connector_id,omitempty"`
	Connector             json.RawMessage `json:"connector,omitempty"`
	Deploy                *bool           `json:"deploy,omitempty"`
	Interface             json.RawMessage `json:"interface,omitempty"`
	Guardrails            json.RawMessage `json:"guardrails,omitempty"`
}

// ModelsRegisterResp represents the register model response.
type ModelsRegisterResp struct {
	TaskID   string `json:"task_id,omitempty"`
	ModelID  string `json:"model_id,omitempty"`
	Status   string `json:"status,omitempty"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r ModelsRegisterResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

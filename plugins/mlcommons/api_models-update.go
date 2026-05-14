// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mlcommons

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// ModelsUpdateReq represents the update model request.
//
// Availability: OpenSearch 2.12+. On 2.11 and earlier the server returns HTTP 405
// (method not allowed) because PUT /_plugins/_ml/models/{model_id} did not exist yet.
//
// Reference: https://docs.opensearch.org/latest/ml-commons-plugin/api/model-apis/update-model/
type ModelsUpdateReq struct {
	ModelID string
	Body    ModelsUpdateBody

	Params ModelsUpdateParams
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ModelsUpdateReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(
		http.MethodPut,
		fmt.Sprintf("/_plugins/_ml/models/%s", r.ModelID),
		bytes.NewReader(body),
		r.Params.get(),
		r.Header,
	)
}

// ModelsUpdateBody represents the JSON body for updating a model.
type ModelsUpdateBody struct {
	Name         string          `json:"name,omitempty"`
	Description  string          `json:"description,omitempty"`
	ModelGroupID string          `json:"model_group_id,omitempty"`
	IsEnabled    *bool           `json:"is_enabled,omitempty"`
	RateLimiter  json.RawMessage `json:"rate_limiter,omitempty"`
	ModelConfig  *ModelConfig    `json:"model_config,omitempty"`
	Connector    json.RawMessage `json:"connector,omitempty"`
	ConnectorID  string          `json:"connector_id,omitempty"`
	Interface    json.RawMessage `json:"interface,omitempty"`
	Guardrails   json.RawMessage `json:"guardrails,omitempty"`
}

// ModelsUpdateResp represents the update model response.
type ModelsUpdateResp struct {
	Index       string `json:"_index,omitempty"`
	ID          string `json:"_id,omitempty"`
	Version     int    `json:"_version,omitempty"`
	Result      string `json:"result,omitempty"`
	SeqNo       int    `json:"_seq_no,omitempty"`
	PrimaryTerm int    `json:"_primary_term,omitempty"`
	Shards      *struct {
		Total      int `json:"total"`
		Successful int `json:"successful"`
		Failed     int `json:"failed"`
	} `json:"_shards,omitempty"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r ModelsUpdateResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

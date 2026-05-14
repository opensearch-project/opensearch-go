// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mlcommons

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// ModelsUndeployReq represents the undeploy model request.
//
// When ModelID is empty, a batch undeploy is issued against /_plugins/_ml/models/_undeploy
// and the Body's ModelIDs and NodeIDs determine targets. When ModelID is set, the request
// targets that model; an optional NodeIDs path segment further restricts the operation.
//
// Version note: on OpenSearch 2.11, undeploy of a non-existent model id silently succeeds;
// from 2.12 onward the server surfaces a structured error.
//
// Reference: https://docs.opensearch.org/latest/ml-commons-plugin/api/model-apis/undeploy-model/
type ModelsUndeployReq struct {
	ModelID string
	NodeIDs []string
	// Body is optional. Used for batch undeploy (ModelID empty) or to scope by node IDs.
	Body *ModelsUndeployBody

	Params ModelsUndeployParams
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ModelsUndeployReq) GetRequest() (*http.Request, error) {
	var (
		body io.Reader
		raw  []byte
		err  error
	)
	if r.Body != nil {
		raw, err = json.Marshal(r.Body)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}

	var path strings.Builder
	path.WriteString("/_plugins/_ml/models")
	if r.ModelID != "" {
		path.WriteString("/")
		path.WriteString(r.ModelID)
		path.WriteString("/_undeploy")
		if len(r.NodeIDs) > 0 {
			path.WriteString("/")
			path.WriteString(strings.Join(r.NodeIDs, ","))
		}
	} else {
		path.WriteString("/_undeploy")
	}

	return opensearch.BuildRequest(
		http.MethodPost,
		path.String(),
		body,
		r.Params.get(),
		r.Header,
	)
}

// ModelsUndeployBody scopes the undeploy to specific node IDs and (for batch undeploy) model IDs.
type ModelsUndeployBody struct {
	NodeIDs  []string `json:"node_ids,omitempty"`
	ModelIDs []string `json:"model_ids,omitempty"`
}

// ModelsUndeployResp represents the undeploy response.
//
// For per-model undeploy the response is an async task envelope; for batch undeploy each
// participating node reports the per-model status. Both shapes are surfaced.
type ModelsUndeployResp struct {
	TaskID   string                                   `json:"task_id,omitempty"`
	Status   string                                   `json:"status,omitempty"`
	Nodes    map[string]map[string]ModelUndeployStats `json:"-"`
	response *opensearch.Response
}

// ModelUndeployStats reports per-node, per-model undeploy outcome.
type ModelUndeployStats struct {
	Stats string `json:"stats,omitempty"`
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r ModelsUndeployResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

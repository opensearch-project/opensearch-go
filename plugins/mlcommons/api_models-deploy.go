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
	"io"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// ModelsDeployReq represents the deploy model request.
//
// Reference: https://docs.opensearch.org/latest/ml-commons-plugin/api/model-apis/deploy-model/
type ModelsDeployReq struct {
	ModelID string
	// Body is optional; when populated it restricts deployment to specific worker nodes.
	Body *ModelsDeployBody

	Params ModelsDeployParams
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ModelsDeployReq) GetRequest() (*http.Request, error) {
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

	return opensearch.BuildRequest(
		http.MethodPost,
		fmt.Sprintf("/_plugins/_ml/models/%s/_deploy", r.ModelID),
		body,
		r.Params.get(),
		r.Header,
	)
}

// ModelsDeployBody scopes deployment to a subset of cluster nodes.
type ModelsDeployBody struct {
	NodeIDs []string `json:"node_ids,omitempty"`
}

// ModelsDeployResp is the async task envelope returned by deploy.
type ModelsDeployResp struct {
	ModelTaskInfo
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r ModelsDeployResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

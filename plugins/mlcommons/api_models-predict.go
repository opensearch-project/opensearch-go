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

// ModelsPredictReq represents the predict request for a deployed model.
//
// Body is shaped per FunctionName (TEXT_EMBEDDING, REMOTE, SPARSE_ENCODING, …) so it is
// surfaced as raw JSON. Callers compose the body with the model-specific schema.
//
// Reference: https://docs.opensearch.org/latest/ml-commons-plugin/api/model-apis/predict/
type ModelsPredictReq struct {
	ModelID string
	Body    json.RawMessage

	Params ModelsPredictParams
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ModelsPredictReq) GetRequest() (*http.Request, error) {
	var body io.Reader
	if len(r.Body) > 0 {
		body = bytes.NewReader(r.Body)
	}

	return opensearch.BuildRequest(
		http.MethodPost,
		fmt.Sprintf("/_plugins/_ml/models/%s/_predict", r.ModelID),
		body,
		r.Params.get(),
		r.Header,
	)
}

// ModelsPredictResp wraps the prediction response.
//
// InferenceResults is captured as raw JSON because shape varies per algorithm. Callers
// typically unmarshal it into an algorithm-specific struct.
type ModelsPredictResp struct {
	InferenceResults json.RawMessage `json:"inference_results,omitempty"`
	Status           string          `json:"status,omitempty"`
	TaskID           string          `json:"task_id,omitempty"`
	response         *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r ModelsPredictResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

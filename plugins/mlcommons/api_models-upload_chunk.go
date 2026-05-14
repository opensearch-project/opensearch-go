// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mlcommons

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// ModelsUploadChunkReq uploads a single chunk of model bytes. ChunkNumber is zero-indexed.
//
// Reference: https://docs.opensearch.org/latest/ml-commons-plugin/api/model-apis/upload-chunk/
type ModelsUploadChunkReq struct {
	ModelID     string
	ChunkNumber int
	Body        []byte

	Params ModelsUploadChunkParams
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client.
//
// Note on Content-Type: OpenSearch validates the request media type strictly and only
// registers application/json for the upload_chunk route, but the action reads the body
// as raw bytes regardless. Sending application/octet-stream results in HTTP 406. We
// therefore rely on the default application/json from opensearch.BuildRequest while
// transmitting raw bytes as the body. Verified against OpenSearch 3.6.
func (r ModelsUploadChunkReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		http.MethodPost,
		fmt.Sprintf("/_plugins/_ml/models/%s/upload_chunk/%d", r.ModelID, r.ChunkNumber),
		bytes.NewReader(r.Body),
		r.Params.get(),
		r.Header,
	)
}

// ModelsUploadChunkResp represents the upload_chunk response.
type ModelsUploadChunkResp struct {
	Status   string `json:"status,omitempty"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r ModelsUploadChunkResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

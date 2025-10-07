// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// ConfigPatchReq represents possible options for the securityconfig patch request
type ConfigPatchReq struct {
	Body ConfigPatchBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ConfigPatchReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(
		"PATCH",
		"/_plugins/_security/api/securityconfig",
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// ConfigPatchResp represents the returned struct of the securityconfig patch response
type ConfigPatchResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r ConfigPatchResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// ConfigPatchBody represents the request body for the securityconfig patch request
type ConfigPatchBody []ConfigPatchBodyItem

// ConfigPatchBodyItem is a sub type of ConfigPatchBody represeting patch item
type ConfigPatchBodyItem struct {
	OP    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

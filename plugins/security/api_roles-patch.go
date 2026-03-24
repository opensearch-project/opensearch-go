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

// RolesPatchReq represents possible options for the roles patch request
type RolesPatchReq struct {
	Role string
	Body RolesPatchBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r RolesPatchReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	path, err := opensearch.ActionSuffixPath{Action: "_plugins/_security/api/roles", Suffix: opensearch.Suffix(r.Role)}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(
		http.MethodPatch,
		path,
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// RolesPatchResp represents the returned struct of the roles patch response
type RolesPatchResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r RolesPatchResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// RolesPatchBody represents the request body for the roles patch request
type RolesPatchBody []RolesPatchBodyItem

// RolesPatchBodyItem is a sub type of RolesPatchBody represeting patch item
type RolesPatchBodyItem struct {
	OP    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

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
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// RolesMappingPatchReq represents possible options for the rolesmapping patch request
type RolesMappingPatchReq struct {
	Role string
	Body RolesMappingPatchBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r RolesMappingPatchReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	var path strings.Builder
	path.Grow(len("/_plugins/_security/api/rolesmapping/") + len(r.Role))
	path.WriteString("/_plugins/_security/api/rolesmapping")
	if len(r.Role) > 0 {
		path.WriteString("/")
		path.WriteString(r.Role)
	}

	return opensearch.BuildRequest(
		"PATCH",
		path.String(),
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// RolesMappingPatchResp represents the returned struct of the rolesmapping patch response
type RolesMappingPatchResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r RolesMappingPatchResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// RolesMappingPatchBody represents the request body for the rolesmapping patch request
type RolesMappingPatchBody []RolesMappingPatchBodyItem

// RolesMappingPatchBodyItem is a sub type of RolesMappingPatchBody represeting patch item
type RolesMappingPatchBodyItem struct {
	OP    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// RolesMappingPutReq represents possible options for the rolesmapping put request
type RolesMappingPutReq struct {
	Role string
	Body RolesMappingPutBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r RolesMappingPutReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(
		"PUT",
		fmt.Sprintf("/_plugins/_security/api/rolesmapping/%s", r.Role),
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// RolesMappingPutBody represents the request body for RolesMappingPutReq
type RolesMappingPutBody struct {
	Hosts           []string `json:"hosts,omitempty"`
	Users           []string `json:"users,omitempty"`
	BackendRoles    []string `json:"backend_roles,omitempty"`
	AndBackendRoles []string `json:"and_backend_roles,omitempty"`
	Description     string   `json:"description,omitempty"`
}

// RolesMappingPutResp represents the returned struct of the rolesmapping put response
type RolesMappingPutResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r RolesMappingPutResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

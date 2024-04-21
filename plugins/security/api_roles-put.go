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

// RolesPutReq represents possible options for the roles put request
type RolesPutReq struct {
	Role string
	Body RolesPutBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r RolesPutReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(
		"PUT",
		fmt.Sprintf("/_plugins/_security/api/roles/%s", r.Role),
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// RolesPutBody represents the request body for RolesPutReq
type RolesPutBody struct {
	Description        string                  `json:"description,omitempty"`
	ClusterPermissions []string                `json:"cluster_permissions,omitempty"`
	IndexPermissions   []RolesIndexPermission  `json:"index_permissions,omitempty"`
	TenantPermissions  []RolesTenantPermission `json:"tenant_permissions,omitempty"`
}

// RolesPutResp represents the returned struct of the roles put response
type RolesPutResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r RolesPutResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

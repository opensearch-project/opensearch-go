// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v3"
)

// RolesGetReq represents possible options for the roles get request
type RolesGetReq struct {
	Role string

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r RolesGetReq) GetRequest() (*http.Request, error) {
	var path strings.Builder
	path.Grow(len("/_plugins/_security/api/roles/") + len(r.Role))
	path.WriteString("/_plugins/_security/api/roles")
	if len(r.Role) > 0 {
		path.WriteString("/")
		path.WriteString(r.Role)
	}

	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		make(map[string]string),
		r.Header,
	)
}

// RolesGetResp represents the returned struct of the roles get response
type RolesGetResp struct {
	Roles    map[string]RolesGetItem
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r RolesGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// RolesGetItem is a sub type of RolesGetResp containing information about a role
type RolesGetItem struct {
	Reserved           bool                    `json:"reserved"`
	Hidden             bool                    `json:"hidden"`
	Description        string                  `json:"description"`
	ClusterPermissions []string                `json:"cluster_permissions"`
	IndexPermissions   []RolesIndexPermission  `json:"index_permissions"`
	TenantPermissions  []RolesTenantPermission `json:"tenant_permissions"`
	Statis             bool                    `json:"static"`
}

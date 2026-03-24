// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// RolesGetReq represents possible options for the roles get request
type RolesGetReq struct {
	Role string

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r RolesGetReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.ActionSuffixPath{Action: "_plugins/_security/api/roles", Suffix: opensearch.Suffix(r.Role)}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(
		http.MethodGet,
		path,
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

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r RolesGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// RolesGetItem is a sub type of RolesGetResp containing information about a role
type RolesGetItem struct {
	Reserved           bool                       `json:"reserved"`
	Hidden             bool                       `json:"hidden"`
	Description        string                     `json:"description"`
	ClusterPermissions []string                   `json:"cluster_permissions"`
	IndexPermissions   []RolesGetIndexPermission  `json:"index_permissions"`
	TenantPermissions  []RolesGetTenantPermission `json:"tenant_permissions"`
	Statis             bool                       `json:"static"`
}

// RolesGetIndexPermission contains index permissions and is used for Get and Put requests
type RolesGetIndexPermission struct {
	IndexPatterns  []string `json:"index_patterns"`
	DLS            string   `json:"dls"`
	FLS            []string `json:"fls"`
	MaskedFields   []string `json:"masked_fields"`
	AllowedActions []string `json:"allowed_actions"`
}

// RolesGetTenantPermission contains tenant permissions and is used for Get and Put requests
type RolesGetTenantPermission struct {
	TenantPatterns []string `json:"tenant_patterns"`
	AllowedActions []string `json:"allowed_actions"`
}

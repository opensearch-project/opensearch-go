// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// RolesMappingGetReq represents possible options for the rolesmapping get request
type RolesMappingGetReq struct {
	Role string

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r RolesMappingGetReq) GetRequest() (*http.Request, error) {
	var path strings.Builder
	path.Grow(len("/_plugins/_security/api/rolesmapping/") + len(r.Role))
	path.WriteString("/_plugins/_security/api/rolesmapping")
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

// RolesMappingGetResp represents the returned struct of the rolesmapping get response
type RolesMappingGetResp struct {
	RolesMapping map[string]RolesMappingGetItem
}

// RolesMappingGetItem is a sub type of RolesMappingGetResp containing information about a role
type RolesMappingGetItem struct {
	Reserved        bool     `json:"reserved"`
	Hidden          bool     `json:"hidden"`
	Hosts           []string `json:"hosts"`
	Users           []string `json:"users"`
	BackendRoles    []string `json:"backend_roles"`
	AndBackendRoles []string `json:"and_backend_roles"`
	Description     string   `json:"description"`
}

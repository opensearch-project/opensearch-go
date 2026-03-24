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

// RolesMappingGetReq represents possible options for the rolesmapping get request
type RolesMappingGetReq struct {
	Role string

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r RolesMappingGetReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.ActionSuffixPath{Action: "_plugins/_security/api/rolesmapping", Suffix: opensearch.Suffix(r.Role)}.Build()
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

// RolesMappingGetResp represents the returned struct of the rolesmapping get response
type RolesMappingGetResp struct {
	RolesMapping map[string]RolesMappingGetItem
	response     *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r RolesMappingGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
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

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

// RolesMappingDeleteReq represents possible options for the roles delete request
type RolesMappingDeleteReq struct {
	Role string

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r RolesMappingDeleteReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.PluginResourcePath{Plugin: "_security", Resource: "rolesmapping", Name: opensearch.Name(r.Role)}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(http.MethodDelete, path, nil, make(map[string]string), r.Header)
}

// RolesMappingDeleteResp represents the returned struct of the roles delete response
type RolesMappingDeleteResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r RolesMappingDeleteResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

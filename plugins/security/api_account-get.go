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

// AccountGetReq represents possible options for the account get request
type AccountGetReq struct {
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r AccountGetReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		"/_plugins/_security/api/account",
		nil,
		make(map[string]string),
		r.Header,
	)
}

// AccountGetResp represents the returned struct of the account get response
type AccountGetResp struct {
	UserName            string          `json:"user_name"`
	IsReserved          bool            `json:"is_reserved"`
	IsHidden            bool            `json:"is_hidden"`
	IsInternaluser      bool            `json:"is_internal_user"`
	BackendRoles        []string        `json:"backend_roles"`
	CustomAttributes    []string        `json:"custom_attribute_names"`
	UserRequestedTenant *string         `json:"user_requested_tenant"`
	Tennants            map[string]bool `json:"tenants"`
	Roles               []string        `json:"roles"`
}

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

// TenantsGetReq represents possible options for the tenants get request
type TenantsGetReq struct {
	Tenant string

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r TenantsGetReq) GetRequest() (*http.Request, error) {
	var path strings.Builder
	path.Grow(len("/_plugins/_security/api/tenants/") + len(r.Tenant))
	path.WriteString("/_plugins/_security/api/tenants")
	if len(r.Tenant) > 0 {
		path.WriteString("/")
		path.WriteString(r.Tenant)
	}

	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		make(map[string]string),
		r.Header,
	)
}

// TenantsGetResp represents the returned struct of the tenants get response
type TenantsGetResp struct {
	Tenants map[string]TenantsGetItem
}

// TenantsGetItem is a sub type of TenantsGetResp containing information about a tenant
type TenantsGetItem struct {
	Reserved    bool   `json:"reserved"`
	Hidden      bool   `json:"hidden"`
	Description string `json:"description"`
	Statis      bool   `json:"static"`
}

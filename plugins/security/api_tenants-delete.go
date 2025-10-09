// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"fmt"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// TenantsDeleteReq represents possible options for the tenants delete request
type TenantsDeleteReq struct {
	Tenant string

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r TenantsDeleteReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"DELETE",
		fmt.Sprintf("/_plugins/_security/api/tenants/%s", r.Tenant),
		nil,
		make(map[string]string),
		r.Header,
	)
}

// TenantsDeleteResp represents the returned struct of the tenants delete response
type TenantsDeleteResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r TenantsDeleteResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

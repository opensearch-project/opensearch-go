// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// TenantsPatchReq represents possible options for the tenants patch request
type TenantsPatchReq struct {
	Tenant string
	Body   TenantsPatchBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r TenantsPatchReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	var path strings.Builder
	path.Grow(len("/_plugins/_security/api/tenants/") + len(r.Tenant))
	path.WriteString("/_plugins/_security/api/tenants")
	if len(r.Tenant) > 0 {
		path.WriteString("/")
		path.WriteString(r.Tenant)
	}

	return opensearch.BuildRequest(
		"PATCH",
		path.String(),
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// TenantsPatchResp represents the returned struct of the tenants patch response
type TenantsPatchResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r TenantsPatchResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// TenantsPatchBody represents the request body for the tenants patch request
type TenantsPatchBody []TenantsPatchBodyItem

// TenantsPatchBodyItem is a sub type of TenantsPatchBody represeting patch item
type TenantsPatchBodyItem struct {
	OP    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

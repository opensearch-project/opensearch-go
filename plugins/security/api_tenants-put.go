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

// TenantsPutReq represents possible options for the tenants put request
type TenantsPutReq struct {
	Tenant string
	Body   TenantsPutBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r TenantsPutReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(
		"PUT",
		fmt.Sprintf("/_plugins/_security/api/tenants/%s", r.Tenant),
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// TenantsPutBody is the request body for the TenantsPutReq
type TenantsPutBody struct {
	Description string `json:"description,omitempty"`
}

// TenantsPutResp represents the returned struct of the tenants put response
type TenantsPutResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r TenantsPutResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

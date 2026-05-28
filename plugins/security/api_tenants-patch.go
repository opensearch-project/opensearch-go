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

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/internal/build"
	ospath "github.com/opensearch-project/opensearch-go/v4/internal/path"
)

// TenantsPatchReq represents possible options for the tenants patch request
type TenantsPatchReq struct {
	Tenant string
	Body   TenantsPatchBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r TenantsPatchReq) GetRequest(method string) (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	var path string
	if r.Tenant == "" {
		path, err = ospath.SecurityPatchTenantsPath{}.Build()
	} else {
		path, err = ospath.SecurityPatchTenantPath{Tenant: r.Tenant}.Build()
	}
	if err != nil {
		return nil, err
	}

	return build.Request(
		method,
		path,
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

// Inspect returns the Inspect type containing the raw *opensearch.Response
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

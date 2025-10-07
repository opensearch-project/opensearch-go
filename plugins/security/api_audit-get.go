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

// AuditGetReq represents possible options for the audit get request
type AuditGetReq struct {
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r AuditGetReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		"/_plugins/_security/api/audit",
		nil,
		make(map[string]string),
		r.Header,
	)
}

// AuditGetResp represents the returned struct of the audit get response
type AuditGetResp struct {
	ReadOnly []string    `json:"_readonly"`
	Config   AuditConfig `json:"config"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r AuditGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

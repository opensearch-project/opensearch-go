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

	"github.com/opensearch-project/opensearch-go/v3"
)

// AuditPatchReq represents possible options for the audit patch request
type AuditPatchReq struct {
	Body AuditPatchBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r AuditPatchReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(
		"PATCH",
		"/_plugins/_security/api/audit",
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// AuditPatchResp represents the returned struct of the audit patch response
type AuditPatchResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r AuditPatchResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// AuditPatchBody represents the request body for the audit patch request
type AuditPatchBody []AuditPatchBodyItem

// AuditPatchBodyItem is a sub type of AuditPatchBody represeting patch item
type AuditPatchBodyItem struct {
	OP    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

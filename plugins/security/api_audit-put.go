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
)

// AuditPutReq represents possible options for the audit put request
type AuditPutReq struct {
	Body AuditPutBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r AuditPutReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(
		"PUT",
		"/_plugins/_security/api/audit/config",
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// AuditPutBody is an alias of AuditConfig uses as request body
type AuditPutBody AuditConfig

// AuditPutResp represents the returned struct of the audit put response
type AuditPutResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r AuditPutResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

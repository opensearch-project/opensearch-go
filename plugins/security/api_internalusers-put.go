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

// InternalUsersPutReq represents possible options for the internalusers put request
type InternalUsersPutReq struct {
	User string
	Body InternalUsersPutBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r InternalUsersPutReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(
		"PUT",
		fmt.Sprintf("/_plugins/_security/api/internalusers/%s", r.User),
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// InternalUsersPutBody represents the request body for InternalUsersPutReq
type InternalUsersPutBody struct {
	Password      string            `json:"password,omitempty"`
	Hash          string            `json:"hash,omitempty"`
	BackendRoles  []string          `json:"backend_roles,omitempty"`
	Attributes    map[string]string `json:"attributes,omitempty"`
	Description   string            `json:"description,omitempty"`
	SecurityRoles []string          `json:"opendistro_security_roles,omitempty"`
}

// InternalUsersPutResp represents the returned struct of the internalusers put response
type InternalUsersPutResp struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

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

// ActionGroupsPutReq represents possible options for the actiongroups put request
type ActionGroupsPutReq struct {
	ActionGroup string
	Body        ActionGroupsPutBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ActionGroupsPutReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(
		"PUT",
		fmt.Sprintf("/_plugins/_security/api/actiongroups/%s", r.ActionGroup),
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// ActionGroupsPutResp represents the returned struct of the actiongroups put response
type ActionGroupsPutResp struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// ActionGroupsPutBody represents the request body for the action groups put request
type ActionGroupsPutBody struct {
	AllowedActions []string `json:"allowed_actions"`
	Type           *string  `json:"type,omitempty"`
	Description    *string  `json:"description,omitempty"`
}

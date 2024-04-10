// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"fmt"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v3"
)

// ActionGroupsDeleteReq represents possible options for the actiongroups delete request
type ActionGroupsDeleteReq struct {
	ActionGroup string

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ActionGroupsDeleteReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"DELETE",
		fmt.Sprintf("/_plugins/_security/api/actiongroups/%s", r.ActionGroup),
		nil,
		make(map[string]string),
		r.Header,
	)
}

// ActionGroupsDeleteResp represents the returned struct of the actiongroups delete response
type ActionGroupsDeleteResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r ActionGroupsDeleteResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

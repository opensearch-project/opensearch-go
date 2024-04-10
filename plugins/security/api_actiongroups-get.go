// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v3"
)

// ActionGroupsGetReq represents possible options for the actiongroups get request
type ActionGroupsGetReq struct {
	Header      http.Header
	ActionGroup string
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ActionGroupsGetReq) GetRequest() (*http.Request, error) {
	var path strings.Builder
	path.Grow(len("/_plugins/_security/api/actiongroups/") + len(r.ActionGroup))
	path.WriteString("/_plugins/_security/api/actiongroups")
	if len(r.ActionGroup) > 0 {
		path.WriteString("/")
		path.WriteString(r.ActionGroup)
	}

	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		make(map[string]string),
		r.Header,
	)
}

// ActionGroupsGetResp represents the returned struct of the actiongroups get response
type ActionGroupsGetResp struct {
	Groups   map[string]ActionGroupsGet
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r ActionGroupsGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// ActionGroupsGet is a sub type of ActionGroupsGetResp represeting information about an action group
type ActionGroupsGet struct {
	Reserved       bool     `json:"reserved"`
	Hidden         bool     `json:"hidden"`
	AllowedActions []string `json:"allowed_actions"`
	Static         bool     `json:"static"`
	Description    string   `json:"description,omitempty"`
	Type           string   `json:"type,omitempty"`
}

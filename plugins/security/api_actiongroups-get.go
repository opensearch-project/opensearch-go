// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"net/http"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/internal/build"
	ospath "github.com/opensearch-project/opensearch-go/v5/internal/path"
)

// ActionGroupsGetReq represents possible options for the actiongroups get request
type ActionGroupsGetReq struct {
	Header      http.Header
	ActionGroup string
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ActionGroupsGetReq) GetRequest(method string) (*http.Request, error) {
	var path string
	var err error
	if r.ActionGroup == "" {
		path, err = ospath.SecurityGetActionGroupsPath{}.Build()
	} else {
		path, err = ospath.SecurityGetActionGroupPath{ActionGroup: r.ActionGroup}.Build()
	}
	if err != nil {
		return nil, err
	}
	return build.Request(method, path, nil, make(map[string]string), r.Header)
}

// ActionGroupsGetResp represents the returned struct of the actiongroups get response
type ActionGroupsGetResp struct {
	Groups   map[string]ActionGroupsGet
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
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

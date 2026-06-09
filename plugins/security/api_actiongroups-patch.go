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

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/internal/build"
	ospath "github.com/opensearch-project/opensearch-go/v5/internal/path"
)

// ActionGroupsPatchReq represents possible options for the actiongroups patch request
type ActionGroupsPatchReq struct {
	ActionGroup string
	Body        ActionGroupsPatchBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ActionGroupsPatchReq) GetRequest(method string) (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	var path string
	if r.ActionGroup == "" {
		path, err = ospath.SecurityPatchActionGroupsPath{}.Build()
	} else {
		path, err = ospath.SecurityPatchActionGroupPath{ActionGroup: r.ActionGroup}.Build()
	}
	if err != nil {
		return nil, err
	}
	return build.Request(method, path, bytes.NewReader(body), make(map[string]string), r.Header)
}

// ActionGroupsPatchResp represents the returned struct of the actiongroups patch response
type ActionGroupsPatchResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r ActionGroupsPatchResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// ActionGroupsPatchBody represents the request body for the action groups patch request
type ActionGroupsPatchBody []ActionGroupsPatchBodyItem

// ActionGroupsPatchBodyItem is a sub type of ActionGroupsPatchBody represeting an action group
type ActionGroupsPatchBodyItem struct {
	OP    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

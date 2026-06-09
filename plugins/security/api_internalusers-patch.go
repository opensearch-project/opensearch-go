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

// InternalUsersPatchReq represents possible options for the internalusers patch request
type InternalUsersPatchReq struct {
	User string
	Body InternalUsersPatchBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r InternalUsersPatchReq) GetRequest(method string) (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	var path string
	if r.User == "" {
		path, err = ospath.SecurityPatchUsersPath{}.Build()
	} else {
		path, err = ospath.SecurityPatchUserPath{Username: r.User}.Build()
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

// InternalUsersPatchResp represents the returned struct of the internalusers patch response
type InternalUsersPatchResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r InternalUsersPatchResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// InternalUsersPatchBody represents the request body for the internalusers patch request
type InternalUsersPatchBody []InternalUsersPatchBodyItem

// InternalUsersPatchBodyItem is a sub type of InternalUsersPatchBody represeting patch item
type InternalUsersPatchBodyItem struct {
	OP    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

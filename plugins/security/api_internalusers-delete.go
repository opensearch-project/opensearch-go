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

// InternalUsersDeleteReq represents possible options for the internalusers delete request
type InternalUsersDeleteReq struct {
	User string

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r InternalUsersDeleteReq) GetRequest(method string) (*http.Request, error) {
	path, err := ospath.SecurityDeleteUserPath{Username: r.User}.Build()
	if err != nil {
		return nil, err
	}

	return build.Request(method, path, nil, make(map[string]string), r.Header)
}

// InternalUsersDeleteResp represents the returned struct of the internalusers delete response
type InternalUsersDeleteResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r InternalUsersDeleteResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

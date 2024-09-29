// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// InternalUsersGetReq represents possible options for the internal users get request
type InternalUsersGetReq struct {
	User string

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r InternalUsersGetReq) GetRequest() (*http.Request, error) {
	var path strings.Builder
	path.Grow(len("/_plugins/_security/api/internalusers/") + len(r.User))
	path.WriteString("/_plugins/_security/api/internalusers")
	if len(r.User) > 0 {
		path.WriteString("/")
		path.WriteString(r.User)
	}

	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		make(map[string]string),
		r.Header,
	)
}

// InternalUsersGetResp represents the returned struct of the internal users get response
type InternalUsersGetResp struct {
	Users map[string]InternalUsersGetItem
}

// InternalUsersGetItem is a sub type of InternalUsersGetResp containing information about a user
type InternalUsersGetItem struct {
	Hash          string            `json:"hash"`
	Reserved      bool              `json:"reserved"`
	Hidden        bool              `json:"hidden"`
	BackendRoles  []string          `json:"backend_roles"`
	Attributes    map[string]string `json:"attributes"`
	Description   string            `json:"description"`
	SecurityRoles []string          `json:"opendistro_security_roles"`
	Statis        bool              `json:"static"`
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"fmt"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// InternalUsersDeleteReq represents possible options for the internalusers delete request
type InternalUsersDeleteReq struct {
	User string

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r InternalUsersDeleteReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"DELETE",
		fmt.Sprintf("/_plugins/_security/api/internalusers/%s", r.User),
		nil,
		make(map[string]string),
		r.Header,
	)
}

// InternalUsersDeleteResp represents the returned struct of the internalusers delete response
type InternalUsersDeleteResp struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

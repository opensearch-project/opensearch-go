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
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// InternalUsersPatchReq represents possible options for the internalusers patch request
type InternalUsersPatchReq struct {
	User string
	Body InternalUsersPatchBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r InternalUsersPatchReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	var path strings.Builder
	path.Grow(len("/_plugins/_security/api/internalusers/") + len(r.User))
	path.WriteString("/_plugins/_security/api/internalusers")
	if len(r.User) > 0 {
		path.WriteString("/")
		path.WriteString(r.User)
	}

	return opensearch.BuildRequest(
		"PATCH",
		path.String(),
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// InternalUsersPatchResp represents the returned struct of the internalusers patch response
type InternalUsersPatchResp struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// InternalUsersPatchBody represents the request body for the internalusers patch request
type InternalUsersPatchBody []InternalUsersPatchBodyItem

// InternalUsersPatchBodyItem is a sub type of InternalUsersPatchBody represeting patch item
type InternalUsersPatchBodyItem struct {
	OP    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

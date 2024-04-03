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

	"github.com/opensearch-project/opensearch-go/v3"
)

// AccountPutReq represents possible options for the account put request
type AccountPutReq struct {
	Body AccountPutBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r AccountPutReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(
		"PUT",
		"/_plugins/_security/api/account",
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// AccountPutBody reperensts the request body for AccountPutReq
type AccountPutBody struct {
	CurrentPassword string `json:"current_password"`
	Password        string `json:"password"`
}

// AccountPutResp represents the returned struct of the account put response
type AccountPutResp struct {
	Message  string `json:"message"`
	Status   string `json:"status"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r AccountPutResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

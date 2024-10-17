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

	"github.com/opensearch-project/opensearch-go/v4"
)

// ConfigPutReq represents possible options for the securityconfig get request
type ConfigPutReq struct {
	Body ConfigPutBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ConfigPutReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(
		"PUT",
		"/_plugins/_security/api/securityconfig/config",
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// ConfigPutBody represents the request body for ConfigPutReq
type ConfigPutBody struct {
	Dynamic ConfigDynamic `json:"dynamic"`
}

// ConfigPutResp represents the returned struct of the securityconfig get response
type ConfigPutResp struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

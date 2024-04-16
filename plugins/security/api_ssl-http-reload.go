// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// SSLHTTPReloadReq represents possible options for the http ssl reload request
type SSLHTTPReloadReq struct {
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r SSLHTTPReloadReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"PUT",
		"/_plugins/_security/api/ssl/http/reloadcerts",
		nil,
		make(map[string]string),
		r.Header,
	)
}

// SSLHTTPReloadResp represents the returned struct of the http ssl reload response
type SSLHTTPReloadResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r SSLHTTPReloadResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

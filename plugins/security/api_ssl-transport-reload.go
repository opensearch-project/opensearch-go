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

// SSLTransportReloadReq represents possible options for the transport ssl reload request
type SSLTransportReloadReq struct {
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r SSLTransportReloadReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"PUT",
		"/_plugins/_security/api/ssl/transport/reloadcerts",
		nil,
		make(map[string]string),
		r.Header,
	)
}

// SSLTransportReloadResp represents the returned struct of the transport ssl reload response
type SSLTransportReloadResp struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

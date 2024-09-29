// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"net/http"
	"time"

	"github.com/opensearch-project/opensearch-go/v4"
)

// SSLGetReq represents possible options for the ssl/certs get request
type SSLGetReq struct {
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r SSLGetReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		"/_plugins/_security/api/ssl/certs",
		nil,
		make(map[string]string),
		r.Header,
	)
}

// SSLGetResp represents the returned struct of the ssl/certs get response
type SSLGetResp struct {
	HTTPCerts     []SSLCertItem `json:"http_certificates_list"`
	TransportCert []SSLCertItem `json:"transport_certificates_list"`
}

// SSLCertItem is a sub type of SSLGetResp containing information about a cert
type SSLCertItem struct {
	IssuerDN  string    `json:"issuer_dn"`
	SubjectDN string    `json:"subject_dn"`
	SAN       string    `json:"san"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
}

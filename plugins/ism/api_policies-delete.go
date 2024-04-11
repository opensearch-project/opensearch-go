// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ism

import (
	"fmt"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v3"
)

// PoliciesDeleteReq represents possible options for the policies get request
type PoliciesDeleteReq struct {
	Policy string

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r PoliciesDeleteReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		http.MethodDelete,
		fmt.Sprintf("/_plugins/_ism/policies/%s", r.Policy),
		nil,
		make(map[string]string),
		r.Header,
	)
}

// PoliciesDeleteResp represents the returned struct of the policies get response
type PoliciesDeleteResp struct {
	Index         string `json:"_index"`
	Type          string `json:"_type"` // Deprecated with opensearch 2.0
	ID            string `json:"_id"`
	Version       int    `json:"_version"`
	Result        string `json:"result"`
	ForcedRefresh bool   `json:"forced_refresh"`
	Shards        struct {
		Total      int `json:"total"`
		Successful int `json:"successful"`
		Failed     int `json:"failed"`
	} `json:"_shards"`
	SeqNo       int `json:"_seq_no"`
	PrimaryTerm int `json:"_primary_term"`
	response    *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r PoliciesDeleteResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

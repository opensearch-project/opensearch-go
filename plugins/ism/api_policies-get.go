// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ism

import (
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// PoliciesGetReq represents possible options for the policies get request
type PoliciesGetReq struct {
	Policy string

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r PoliciesGetReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.ActionSuffixPath{Action: "_plugins/_ism/policies", Suffix: opensearch.Suffix(r.Policy)}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(http.MethodGet, path, nil, make(map[string]string), r.Header)
}

// PoliciesGetResp represents the returned struct of the policies get response
type PoliciesGetResp struct {
	Policies      []Policy    `json:"policies,omitempty"`
	TotalPolicies *int        `json:"total_policies,omitempty"`
	ID            *string     `json:"_id,omitempty"`
	SeqNo         *int        `json:"_seq_no,omitempty"`
	PrimaryTerm   *int        `json:"_primary_term,omitempty"`
	Version       *int        `json:"_version,omitempty"`
	Policy        *PolicyBody `json:"policy,omitempty"`
	response      *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r PoliciesGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

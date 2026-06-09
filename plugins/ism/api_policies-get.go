// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ism

import (
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/internal/build"
	ospath "github.com/opensearch-project/opensearch-go/v4/internal/path"
)

// PoliciesGetReq represents possible options for the policies get request
type PoliciesGetReq struct {
	Policy string

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r PoliciesGetReq) GetRequest(method string) (*http.Request, error) {
	var path string
	var err error
	if r.Policy == "" {
		path, err = ospath.ISMGetPoliciesPath{}.Build()
	} else {
		path, err = ospath.ISMGetPolicyPath{PolicyID: r.Policy}.Build()
	}
	if err != nil {
		return nil, err
	}

	return build.Request(method, path, nil, make(map[string]string), r.Header)
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

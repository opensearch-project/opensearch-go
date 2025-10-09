// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ism

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// PoliciesPutReq represents possible options for the policies get request
type PoliciesPutReq struct {
	Policy string
	Body   PoliciesPutBody

	Params PoliciesPutParams
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r PoliciesPutReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(
		http.MethodPut,
		fmt.Sprintf("/_plugins/_ism/policies/%s", r.Policy),
		bytes.NewReader(body),
		r.Params.get(),
		r.Header,
	)
}

// PoliciesPutResp represents the returned struct of the policies get response
type PoliciesPutResp struct {
	ID          string `json:"_id"`
	SeqNo       int    `json:"_seq_no"`
	PrimaryTerm int    `json:"_primary_term"`
	Version     int    `json:"_version"`
	Policy      struct {
		Policy PolicyBody `json:"policy"`
	} `json:"policy"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r PoliciesPutResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// PoliciesPutBody represents the request body for the policies put request
type PoliciesPutBody Policy

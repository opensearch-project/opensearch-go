// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// NodesDNPutReq represents possible options for the nodesdn put request
type NodesDNPutReq struct {
	Cluster string
	Body    NodesDNPutBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r NodesDNPutReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(
		"PUT",
		fmt.Sprintf("/_plugins/_security/api/nodesdn/%s", r.Cluster),
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// NodesDNPutBody reperensts the request body for NodesDNPutReq
type NodesDNPutBody struct {
	NodesDN []string `json:"nodes_dn"`
}

// NodesDNPutResp represents the returned struct of the nodesdn put response
type NodesDNPutResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r NodesDNPutResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

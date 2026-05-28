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
	"github.com/opensearch-project/opensearch-go/v4/internal/build"
	ospath "github.com/opensearch-project/opensearch-go/v4/internal/path"
)

// NodesDNPatchReq represents possible options for the nodesdn patch request
type NodesDNPatchReq struct {
	Cluster string
	Body    NodesDNPatchBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r NodesDNPatchReq) GetRequest(method string) (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	var path string
	if r.Cluster == "" {
		path, err = ospath.SecurityPatchDistinguishedNamesPath{}.Build()
	} else {
		path, err = ospath.SecurityPatchDistinguishedNamePath{ClusterName: r.Cluster}.Build()
	}
	if err != nil {
		return nil, err
	}

	return build.Request(
		method,
		path,
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// NodesDNPatchResp represents the returned struct of the nodesdn patch response
type NodesDNPatchResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r NodesDNPatchResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// NodesDNPatchBody represents the request body for the nodesdn patch request
type NodesDNPatchBody []NodesDNPatchBodyItem

// NodesDNPatchBodyItem is a sub type of NodesDNPatchBody represeting patch item
type NodesDNPatchBodyItem struct {
	OP    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

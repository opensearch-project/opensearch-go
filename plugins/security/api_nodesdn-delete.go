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

// NodesDNDeleteReq represents possible options for the nodesdn delete request
type NodesDNDeleteReq struct {
	Cluster string

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r NodesDNDeleteReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.PluginResourcePath{Plugin: "_security", Resource: "nodesdn", Name: opensearch.Name(r.Cluster)}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(http.MethodDelete, path, nil, make(map[string]string), r.Header)
}

// NodesDNDeleteResp represents the returned struct of the nodesdn delete response
type NodesDNDeleteResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r NodesDNDeleteResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"net/http"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/internal/build"
	ospath "github.com/opensearch-project/opensearch-go/v5/internal/path"
)

// NodesDNDeleteReq represents possible options for the nodesdn delete request
type NodesDNDeleteReq struct {
	Cluster string

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r NodesDNDeleteReq) GetRequest(method string) (*http.Request, error) {
	path, err := ospath.SecurityDeleteDistinguishedNamePath{ClusterName: r.Cluster}.Build()
	if err != nil {
		return nil, err
	}

	return build.Request(method, path, nil, make(map[string]string), r.Header)
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

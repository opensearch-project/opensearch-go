// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"fmt"
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
	return opensearch.BuildRequest(
		"DELETE",
		fmt.Sprintf("/_plugins/_security/api/nodesdn/%s", r.Cluster),
		nil,
		make(map[string]string),
		r.Header,
	)
}

// NodesDNDeleteResp represents the returned struct of the nodesdn delete response
type NodesDNDeleteResp struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

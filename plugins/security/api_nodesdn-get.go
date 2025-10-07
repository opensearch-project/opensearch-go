// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// NodesDNGetReq represents possible options for the nodes dn get request
type NodesDNGetReq struct {
	Cluster string
	Header  http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r NodesDNGetReq) GetRequest() (*http.Request, error) {
	var path strings.Builder
	path.Grow(len("/_plugins/_security/api/nodesdn/") + len(r.Cluster))
	path.WriteString("/_plugins/_security/api/nodesdn")
	if len(r.Cluster) > 0 {
		path.WriteString("/")
		path.WriteString(r.Cluster)
	}

	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		make(map[string]string),
		r.Header,
	)
}

// NodesDNGetResp represents the returned struct of the nodes dn get response
type NodesDNGetResp struct {
	DistinguishedNames map[string]struct {
		NodesDN []string `json:"nodes_dn"`
	}
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r NodesDNGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

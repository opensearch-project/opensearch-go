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
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// NodesDNPatchReq represents possible options for the nodesdn patch request
type NodesDNPatchReq struct {
	Cluster string
	Body    NodesDNPatchBody

	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r NodesDNPatchReq) GetRequest() (*http.Request, error) {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return nil, err
	}

	var path strings.Builder
	path.Grow(len("/_plugins/_security/api/nodesdn/") + len(r.Cluster))
	path.WriteString("/_plugins/_security/api/nodesdn")
	if len(r.Cluster) > 0 {
		path.WriteString("/")
		path.WriteString(r.Cluster)
	}

	return opensearch.BuildRequest(
		"PATCH",
		path.String(),
		bytes.NewReader(body),
		make(map[string]string),
		r.Header,
	)
}

// NodesDNPatchResp represents the returned struct of the nodesdn patch response
type NodesDNPatchResp struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// NodesDNPatchBody represents the request body for the nodesdn patch request
type NodesDNPatchBody []NodesDNPatchBodyItem

// NodesDNPatchBodyItem is a sub type of NodesDNPatchBody represeting patch item
type NodesDNPatchBodyItem struct {
	OP    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

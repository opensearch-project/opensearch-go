// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"io"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// SnapshotRestoreReq represents possible options for the index create request
type SnapshotRestoreReq struct {
	Repo     string
	Snapshot string

	Body io.Reader

	Header http.Header
	Params SnapshotRestoreParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r SnapshotRestoreReq) GetRequest() (*http.Request, error) {
	path, err := opensearch.SnapshotActionPath{
		Repo:     opensearch.Repo(r.Repo),
		Snapshot: opensearch.Snapshot(r.Snapshot),
		Action:   "_restore",
	}.Build()
	if err != nil {
		return nil, err
	}

	return opensearch.BuildRequest(http.MethodPost, path, r.Body, r.Params.get(), r.Header)
}

// SnapshotRestoreResp represents the returned struct of the index create response
type SnapshotRestoreResp struct {
	Accepted bool `json:"accepted"`
	Snapshot struct {
		Snapshot string   `json:"snapshot"`
		Indices  []string `json:"indices"`
		Shards   struct {
			Total      int `json:"total"`
			Failed     int `json:"failed"`
			Successful int `json:"successful"`
		} `json:"shards"`
	} `json:"snapshot"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r SnapshotRestoreResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

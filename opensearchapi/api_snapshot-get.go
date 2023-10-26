// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package opensearchapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
)

// SnapshotGetReq represents possible options for the index create request
type SnapshotGetReq struct {
	Repo      string
	Snapshots []string

	Body io.Reader

	Header http.Header
	Params SnapshotGetParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r SnapshotGetReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		fmt.Sprintf("/_snapshot/%s/%s", r.Repo, strings.Join(r.Snapshots, ",")),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// SnapshotGetResp represents the returned struct of the index create response
type SnapshotGetResp struct {
	Snapshots []struct {
		Snapshot                    string            `json:"snapshot"`
		UUID                        string            `json:"uuid"`
		VersionID                   int               `json:"version_id"`
		Version                     string            `json:"version"`
		RemoteStoreIndexShallowCopy bool              `json:"remote_store_index_shallow_copy"`
		Indices                     []string          `json:"indices"`
		DataStreams                 []json.RawMessage `json:"data_streams"`
		IncludeGlobalState          bool              `json:"include_global_state"`
		Metadata                    map[string]string `json:"metadata"`
		State                       string            `json:"state"`
		StartTime                   string            `json:"start_time"`
		StartTimeInMillis           int64             `json:"start_time_in_millis"`
		EndTime                     string            `json:"end_time"`
		EndTimeInMillis             int64             `json:"end_time_in_millis"`
		DurationInMillis            int               `json:"duration_in_millis"`
		Failures                    []json.RawMessage `json:"failures"`
		Shards                      struct {
			Total      int `json:"total"`
			Failed     int `json:"failed"`
			Successful int `json:"successful"`
		} `json:"shards"`
	} `json:"snapshots"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r SnapshotGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

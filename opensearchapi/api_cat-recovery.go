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
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
)

// CatRecoveryReq represent possible options for the /_cat/recovery request
type CatRecoveryReq struct {
	Indices []string
	Header  http.Header
	Params  CatRecoveryParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r CatRecoveryReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")
	var path strings.Builder
	path.Grow(len("/_cat/recovery/") + len(indices))
	path.WriteString("/_cat/recovery")
	if len(r.Indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}
	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// CatRecoveryResp represents the returned struct of the /_cat/recovery response
type CatRecoveryResp struct {
	Recovery []CatRecoveryItemResp
	response *opensearch.Response
}

// CatRecoveryItemResp represents one index of the CatRecoveryResp
type CatRecoveryItemResp struct {
	Index                string `json:"index"`
	Shard                int    `json:"shard,string"`
	StartTime            string `json:"start_time"`
	StartTimeMillis      int    `json:"start_time_millis,string"`
	StopTime             string `json:"stop_time"`
	StopTimeMillis       int    `json:"stop_time_millis,string"`
	Time                 string `json:"time"`
	Type                 string `json:"type"`
	Stage                string `json:"stage"`
	SourceHost           string `json:"source_host"`
	SourceNode           string `json:"source_node"`
	TargetHost           string `json:"target_host"`
	TargetNode           string `json:"target_node"`
	Repository           string `json:"repository"`
	Snapshot             string `json:"snapshot"`
	Files                int    `json:"files,string"`
	FilesRecovered       int    `json:"files_recovered,string"`
	FilesPercent         string `json:"files_percent"`
	FilesTotal           int    `json:"files_total,string"`
	Bytes                int    `json:"bytes,string"`
	BytesRecovered       int    `json:"bytes_recovered,string"`
	BytesPercent         string `json:"bytes_percent"`
	BytesTotal           int    `json:"bytes_total,string"`
	TranslogOps          int    `json:"translog_ops,string"`
	TranslogOpsRecovered int    `json:"translog_ops_recovered,string"`
	TranslogOpsPercent   string `json:"translog_ops_percent"`
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r CatRecoveryResp) Inspect() Inspect {
	return Inspect{
		Response: r.response,
	}
}

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

// IndicesRecoveryReq represents possible options for the index shrink request
type IndicesRecoveryReq struct {
	Indices []string

	Header http.Header
	Params IndicesRecoveryParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IndicesRecoveryReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")

	var path strings.Builder
	path.Grow(11 + len(indices))
	if len(indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}
	path.WriteString("/_recovery")
	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// IndicesRecoveryResp represents the returned struct of the index shrink response
type IndicesRecoveryResp struct {
	Indices map[string]struct {
		Shards []struct {
			ID                int                     `json:"id"`
			Type              string                  `json:"type"`
			Stage             string                  `json:"stage"`
			Primary           bool                    `json:"primary"`
			StartTimeInMillis int64                   `json:"start_time_in_millis"`
			StopTimeInMillis  int64                   `json:"stop_time_in_millis"`
			TotalTimeInMillis int                     `json:"total_time_in_millis"`
			Source            IndicesRecoveryNodeInfo `json:"source"`
			Target            IndicesRecoveryNodeInfo `json:"target"`
			Index             struct {
				Size struct {
					TotalInBytes     int    `json:"total_in_bytes"`
					ReusedInBytes    int    `json:"reused_in_bytes"`
					RecoveredInBytes int    `json:"recovered_in_bytes"`
					Percent          string `json:"percent"`
				} `json:"size"`
				Files struct {
					Total     int    `json:"total"`
					Reused    int    `json:"reused"`
					Recovered int    `json:"recovered"`
					Percent   string `json:"percent"`
				} `json:"files"`
				TotalTimeInMillis          int `json:"total_time_in_millis"`
				SourceThrottleTimeInMillis int `json:"source_throttle_time_in_millis"`
				TargetThrottleTimeInMillis int `json:"target_throttle_time_in_millis"`
			} `json:"index"`
			Translog struct {
				Recovered         int    `json:"recovered"`
				Total             int    `json:"total"`
				Percent           string `json:"percent"`
				TotalOnStart      int    `json:"total_on_start"`
				TotalTimeInMillis int    `json:"total_time_in_millis"`
			} `json:"translog"`
			VerifyIndex struct {
				CheckIndexTimeInMillis int `json:"check_index_time_in_millis"`
				TotalTimeInMillis      int `json:"total_time_in_millis"`
			} `json:"verify_index"`
		} `json:"shards"`
	}
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r IndicesRecoveryResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// IndicesRecoveryNodeInfo is a sub type of IndicesRecoveryResp represeing Node information
type IndicesRecoveryNodeInfo struct {
	ID               string `json:"id"`
	Host             string `json:"host"`
	TransportAddress string `json:"transport_address"`
	IP               string `json:"ip"`
	Name             string `json:"name"`
}

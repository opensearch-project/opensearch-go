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
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v2"
)

// PointInTimeDeleteReq represents possible options for the index create request
type PointInTimeDeleteReq struct {
	PitID []string

	Header http.Header
	Params PointInTimeDeleteParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r PointInTimeDeleteReq) GetRequest() (*http.Request, error) {
	var body io.Reader
	if len(r.PitID) > 0 {
		bodyStruct := PointInTimeDeleteRequestBody{PitID: r.PitID}
		bodyJSON, err := json.Marshal(bodyStruct)
		if err != nil {
			return nil, err
		}
		body = bytes.NewBuffer(bodyJSON)
	}

	return opensearch.BuildRequest(
		"DELETE",
		"/_search/point_in_time",
		body,
		r.Params.get(),
		r.Header,
	)
}

// PointInTimeDeleteRequestBody is used to from the delete request body
type PointInTimeDeleteRequestBody struct {
	PitID []string `json:"pit_id"`
}

// PointInTimeDeleteResp represents the returned struct of the index create response
type PointInTimeDeleteResp struct {
	Pits []struct {
		PitID      string `json:"pit_id"`
		Successful bool   `json:"successful"`
	} `json:"pits"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r PointInTimeDeleteResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

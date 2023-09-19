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

// DataStreamGetReq represents possible options for the _data_stream get request
type DataStreamGetReq struct {
	DataStreams []string

	Header http.Header
	Params DataStreamGetParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r DataStreamGetReq) GetRequest() (*http.Request, error) {
	dataStreams := strings.Join(r.DataStreams, ",")

	var path strings.Builder
	path.Grow(len("/_data_stream/") + len(dataStreams))
	path.WriteString("/_data_stream")
	if len(r.DataStreams) > 0 {
		path.WriteString("/")
		path.WriteString(dataStreams)
	}

	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// DataStreamGetResp represents the returned struct of the _data_stream get response
type DataStreamGetResp struct {
	DataStreams []DataStreamGetDetails `json:"data_streams"`
	response    *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r DataStreamGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// DataStreamGetDetails is a sub type if DataStreamGetResp containing information about a data stream
type DataStreamGetDetails struct {
	Name           string `json:"name"`
	TimestampField struct {
		Name string `json:"name"`
	} `json:"timestamp_field"`
	Indices    []DataStreamIndices `json:"indices"`
	Generation int                 `json:"generation"`
	Status     string              `json:"status"`
	Template   string              `json:"template"`
}

// DataStreamIndices is a sub type of DataStreamGetDetails containing information about an index
type DataStreamIndices struct {
	Name string `json:"index_name"`
	UUID string `json:"index_uuid"`
}

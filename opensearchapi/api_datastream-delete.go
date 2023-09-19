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
	"fmt"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v2"
)

// DataStreamDeleteReq represents possible options for the index _data_stream delete request
type DataStreamDeleteReq struct {
	DataStream string

	Header http.Header
	Params DataStreamDeleteParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r DataStreamDeleteReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"DELETE",
		fmt.Sprintf("/_data_stream/%s", r.DataStream),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// DataStreamDeleteResp represents the returned struct of the _data_stream delete response
type DataStreamDeleteResp struct {
	Acknowledged bool `json:"acknowledged"`
	response     *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r DataStreamDeleteResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

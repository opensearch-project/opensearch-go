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
	"io"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v2"
)

// IndicesSplitReq represents possible options for the index split request
type IndicesSplitReq struct {
	Index  string
	Target string

	Body io.Reader

	Header http.Header
	Params IndicesSplitParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r IndicesSplitReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"POST",
		fmt.Sprintf("/%s/_split/%s", r.Index, r.Target),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// IndicesSplitResp represents the returned struct of the index split response
type IndicesSplitResp struct {
	Acknowledged       bool   `json:"acknowledged"`
	ShardsAcknowledged bool   `json:"shards_acknowledged"`
	Index              string `json:"index"`
	response           *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r IndicesSplitResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

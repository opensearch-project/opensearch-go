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
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v2"
)

// Reindex executes a / request with the optional ReindexReq
func (c Client) Reindex(ctx context.Context, req ReindexReq) (*ReindexResp, error) {
	var (
		data ReindexResp
		err  error
	)
	if data.response, err = c.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// ReindexReq represents possible options for the / request
type ReindexReq struct {
	Body io.Reader

	Header http.Header
	Params ReindexParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ReindexReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"POST",
		"/_reindex",
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// ReindexResp represents the returned struct of the / response
type ReindexResp struct {
	Took             int  `json:"took"`
	TimedOut         bool `json:"timed_out"`
	Total            int  `json:"total"`
	Updated          int  `json:"updated"`
	Created          int  `json:"created"`
	Deleted          int  `json:"deleted"`
	Batches          int  `json:"batches"`
	VersionConflicts int  `json:"version_conflicts"`
	Noops            int  `json:"noops"`
	Retries          struct {
		Bulk   int `json:"bulk"`
		Search int `json:"search"`
	} `json:"retries"`
	ThrottledMillis      int               `json:"throttled_millis"`
	RequestsPerSecond    float64           `json:"requests_per_second"`
	ThrottledUntilMillis int               `json:"throttled_until_millis"`
	Failures             []json.RawMessage `json:"failures"`
	Task                 string            `json:"task"`
	response             *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r ReindexResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

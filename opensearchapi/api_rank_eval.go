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
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
)

// RankEval executes a /_rank_eval request with the required RankEvalReq
func (c Client) RankEval(ctx context.Context, req RankEvalReq) (*RankEvalResp, error) {
	var (
		data RankEvalResp
		err  error
	)
	if data.response, err = c.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// RankEvalReq represents possible options for the /_rank_eval request
type RankEvalReq struct {
	Indices []string

	Body io.Reader

	Header http.Header
	Params RankEvalParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r RankEvalReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")
	var path strings.Builder
	path.Grow(len("//_rank_eval") + len(indices))
	if len(r.Indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}
	path.WriteString("/_rank_eval")
	return opensearch.BuildRequest(
		"GET",
		path.String(),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// RankEvalResp represents the returned struct of the /_rank_eval response
type RankEvalResp struct {
	MetricScore float64         `json:"metric_score"`
	Details     json.RawMessage `json:"details"`
	Failures    json.RawMessage `json:"failures"`
	response    *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r RankEvalResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

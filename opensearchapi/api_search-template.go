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
	"io"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
)

// SearchTemplate executes a /_search request with the optional SearchTemplateReq
func (c Client) SearchTemplate(ctx context.Context, req SearchTemplateReq) (*SearchTemplateResp, error) {
	var (
		data SearchTemplateResp
		err  error
	)
	if data.response, err = c.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// SearchTemplateReq represents possible options for the /_search request
type SearchTemplateReq struct {
	Indices []string

	Body io.Reader

	Header http.Header
	Params SearchTemplateParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r SearchTemplateReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")
	var path strings.Builder
	path.Grow(len("//_search/template") + len(indices))
	if len(r.Indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}
	path.WriteString("/_search/template")
	return opensearch.BuildRequest(
		"POST",
		path.String(),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// SearchTemplateResp represents the returned struct of the /_search response
type SearchTemplateResp struct {
	Took    int            `json:"took"`
	Timeout bool           `json:"timed_out"`
	Shards  ResponseShards `json:"_shards"`
	Hits    struct {
		Total struct {
			Value    int    `json:"value"`
			Relation string `json:"relation"`
		} `json:"total"`
		MaxScore *float32    `json:"max_score"`
		Hits     []SearchHit `json:"hits"`
	} `json:"hits"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r SearchTemplateResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

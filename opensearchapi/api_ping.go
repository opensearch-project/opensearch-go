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
	"net/http"

	"github.com/opensearch-project/opensearch-go/v2"
)

// Ping executes a / request with the optional PingReq
func (c Client) Ping(ctx context.Context, req *PingReq) (*opensearch.Response, error) {
	if req == nil {
		req = &PingReq{}
	}

	return c.do(ctx, req, nil)
}

// PingReq represents possible options for the / request
type PingReq struct {
	Header http.Header
	Params PingParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r PingReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"HEAD",
		"/",
		nil,
		r.Params.get(),
		r.Header,
	)
}

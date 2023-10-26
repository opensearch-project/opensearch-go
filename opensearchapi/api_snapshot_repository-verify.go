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

// SnapshotRepositoryVerifyReq represents possible options for the index create request
type SnapshotRepositoryVerifyReq struct {
	Repo string

	Header http.Header
	Params SnapshotRepositoryVerifyParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r SnapshotRepositoryVerifyReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"POST",
		fmt.Sprintf("/_snapshot/%s/_verify", r.Repo),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// SnapshotRepositoryVerifyResp represents the returned struct of the index create response
type SnapshotRepositoryVerifyResp struct {
	Nodes map[string]struct {
		Name string `json:"name"`
	} `json:"nodes"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r SnapshotRepositoryVerifyResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

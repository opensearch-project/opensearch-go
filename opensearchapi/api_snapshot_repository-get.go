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

// SnapshotRepositoryGetReq represents possible options for the index create request
type SnapshotRepositoryGetReq struct {
	Repos []string

	Header http.Header
	Params SnapshotRepositoryGetParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r SnapshotRepositoryGetReq) GetRequest() (*http.Request, error) {
	repos := strings.Join(r.Repos, ",")

	var path strings.Builder
	path.Grow(len("/_snapshot/") + len(repos))
	path.WriteString("/_snapshot")
	if len(r.Repos) > 0 {
		path.WriteString("/")
		path.WriteString(repos)
	}

	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// SnapshotRepositoryGetResp represents the returned struct of the index create response
type SnapshotRepositoryGetResp struct {
	Repos map[string]struct {
		Type     string            `json:"type"`
		Settings map[string]string `json:"settings"`
	}
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r SnapshotRepositoryGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

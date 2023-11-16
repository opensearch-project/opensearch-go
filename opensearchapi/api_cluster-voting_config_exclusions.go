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

	"github.com/opensearch-project/opensearch-go/v2"
)

// ClusterPostVotingConfigExclusionsReq represents possible options for the /_cluster/voting_config_exclusions request
type ClusterPostVotingConfigExclusionsReq struct {
	Header http.Header
	Params ClusterPostVotingConfigExclusionsParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ClusterPostVotingConfigExclusionsReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"POST",
		"/_cluster/voting_config_exclusions",
		nil,
		r.Params.get(),
		r.Header,
	)
}

// ClusterDeleteVotingConfigExclusionsReq represents possible options for the /_cluster/voting_config_exclusions request
type ClusterDeleteVotingConfigExclusionsReq struct {
	Header http.Header
	Params ClusterDeleteVotingConfigExclusionsParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ClusterDeleteVotingConfigExclusionsReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"DELETE",
		"/_cluster/voting_config_exclusions",
		nil,
		r.Params.get(),
		r.Header,
	)
}

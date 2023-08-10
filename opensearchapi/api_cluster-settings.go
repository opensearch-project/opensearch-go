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
	"encoding/json"
	"io"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v2"
)

// ClusterGetSettingsReq represents possible options for the /_cluster/settings request
type ClusterGetSettingsReq struct {
	Header http.Header
	Params ClusterGetSettingsParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ClusterGetSettingsReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		"/_cluster/settings",
		nil,
		r.Params.get(),
		r.Header,
	)
}

// ClusterGetSettingsResp represents the returned struct of the ClusterGetSettingsReq response
type ClusterGetSettingsResp struct {
	Persistent json.RawMessage `json:"persistent"`
	Transient  json.RawMessage `json:"transient"`
	Defaults   json.RawMessage `json:"defaults"`
	response   *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r ClusterGetSettingsResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// ClusterPutSettingsReq represents possible options for the /_cluster/settings request
type ClusterPutSettingsReq struct {
	Body   io.Reader
	Header http.Header
	Params ClusterPutSettingsParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ClusterPutSettingsReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"PUT",
		"/_cluster/settings",
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// ClusterPutSettingsResp represents the returned struct of the /_cluster/settings response
type ClusterPutSettingsResp struct {
	Acknowledged bool            `json:"acknowledged"`
	Persistent   json.RawMessage `json:"persistent"`
	Transient    json.RawMessage `json:"transient"`
	response     *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r ClusterPutSettingsResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

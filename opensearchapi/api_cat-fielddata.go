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

// CatFieldDataReq represent possible options for the /_cat/fielddata request
type CatFieldDataReq struct {
	FieldData []string
	Header    http.Header
	Params    CatFieldDataParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r CatFieldDataReq) GetRequest() (*http.Request, error) {
	fielddata := strings.Join(r.FieldData, ",")
	var path strings.Builder
	path.Grow(len("/_cat/fielddata/") + len(fielddata))
	path.WriteString("/_cat/fielddata")
	if len(r.FieldData) > 0 {
		path.WriteString("/")
		path.WriteString(fielddata)
	}
	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// CatFieldDataResp represents the returned struct of the /_cat/fielddata response
type CatFieldDataResp struct {
	FieldData []CatFieldDataItemResp
	response  *opensearch.Response
}

// CatFieldDataItemResp represents one index of the CatFieldDataResp
type CatFieldDataItemResp struct {
	ID    string `json:"id"`
	Host  string `json:"host"`
	IP    string `json:"ip"`
	Node  string `json:"node"`
	Field string `json:"field"`
	Size  string `json:"size"`
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r CatFieldDataResp) Inspect() Inspect {
	return Inspect{
		Response: r.response,
	}
}

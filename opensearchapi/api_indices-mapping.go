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

type mappingClient struct {
	apiClient *Client
}

// Get executes a get mapping request with the required MappingGetReq
func (c mappingClient) Get(ctx context.Context, req *MappingGetReq) (*MappingGetResp, error) {
	if req == nil {
		req = &MappingGetReq{}
	}

	var (
		data MappingGetResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data.Indices); err != nil {
		return &data, err
	}

	return &data, nil
}

// Put executes a put mapping request with the required MappingPutReq
func (c mappingClient) Put(ctx context.Context, req MappingPutReq) (*MappingPutResp, error) {
	var (
		data MappingPutResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Field executes a field mapping request with the optional MappingFieldReq
func (c mappingClient) Field(ctx context.Context, req *MappingFieldReq) (*MappingFieldResp, error) {
	if req == nil {
		req = &MappingFieldReq{}
	}

	var (
		data MappingFieldResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// MappingGetReq represents possible options for the mapping get request
type MappingGetReq struct {
	Indices []string

	Header http.Header
	Params MappingGetParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r MappingGetReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")

	var path strings.Builder
	path.Grow(10 + len(indices))
	if len(indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}
	path.WriteString("/_mapping")
	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// MappingGetResp represents the returned struct of the mapping get response
type MappingGetResp struct {
	Indices map[string]struct {
		Mappings json.RawMessage `json:"mappings"`
	}
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r MappingGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// MappingPutReq represents possible options for the mapping put request
type MappingPutReq struct {
	Indices []string

	Body io.Reader

	Header http.Header
	Params MappingPutParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r MappingPutReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")

	var path strings.Builder
	path.Grow(10 + len(indices))
	path.WriteString("/")
	path.WriteString(indices)
	path.WriteString("/_mapping")
	return opensearch.BuildRequest(
		"PUT",
		path.String(),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// MappingPutResp represents the returned struct of the mapping put response
type MappingPutResp struct {
	Acknowledged bool `json:"acknowledged"`
	response     *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r MappingPutResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// MappingFieldReq represents possible options for the mapping field request
type MappingFieldReq struct {
	Indices []string
	Fields  []string

	Header http.Header
	Params MappingPutParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r MappingFieldReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")
	fields := strings.Join(r.Fields, ",")

	var path strings.Builder
	path.Grow(17 + len(indices) + len(fields))
	if len(indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}
	path.WriteString("/_mapping/field/")
	path.WriteString(fields)
	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// MappingFieldResp represents the returned struct of the mapping field response
type MappingFieldResp struct {
	Indices map[string]struct {
		Mappings json.RawMessage `json:"mappings"`
	}
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Reponse
func (r MappingFieldResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

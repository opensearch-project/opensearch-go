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

	"github.com/opensearch-project/opensearch-go/v2"
)

type componentTemplateClient struct {
	apiClient *Client
}

// Create executes a creade componentTemplate request with the required ComponentTemplateCreateReq
func (c componentTemplateClient) Create(ctx context.Context, req ComponentTemplateCreateReq) (*ComponentTemplateCreateResp, error) {
	var (
		data ComponentTemplateCreateResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Delete executes a delete componentTemplate request with the required ComponentTemplateDeleteReq
func (c componentTemplateClient) Delete(ctx context.Context, req ComponentTemplateDeleteReq) (*ComponentTemplateDeleteResp, error) {
	var (
		data ComponentTemplateDeleteResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Get executes a get componentTemplate request with the optional ComponentTemplateGetReq
func (c componentTemplateClient) Get(ctx context.Context, req *ComponentTemplateGetReq) (*ComponentTemplateGetResp, error) {
	if req == nil {
		req = &ComponentTemplateGetReq{}
	}

	var (
		data ComponentTemplateGetResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Exists executes a exists componentTemplate request with the required ComponentTemplatExistsReq
func (c componentTemplateClient) Exists(ctx context.Context, req ComponentTemplateExistsReq) (*opensearch.Response, error) {
	return c.apiClient.do(ctx, req, nil)
}

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

type templateClient struct {
	apiClient *Client
}

// Create executes a creade template request with the required TemplateCreateReq
// Deprecated: uses legacy API (/_template), correct API is /_index_template, use IndexTemplate.Create instread
func (c templateClient) Create(ctx context.Context, req TemplateCreateReq) (*TemplateCreateResp, error) {
	var (
		data TemplateCreateResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Delete executes a delete template request with the required TemplateDeleteReq
// Deprecated: uses legacy API (/_template), correct API is /_index_template, use IndexTemplate.Delete instread
func (c templateClient) Delete(ctx context.Context, req TemplateDeleteReq) (*TemplateDeleteResp, error) {
	var (
		data TemplateDeleteResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data); err != nil {
		return &data, err
	}

	return &data, nil
}

// Get executes a get template request with the optional TemplateGetReq
// Deprecated: uses legacy API (/_template), correct API is /_index_template, use IndexTemplate.Get instread
func (c templateClient) Get(ctx context.Context, req *TemplateGetReq) (*TemplateGetResp, error) {
	if req == nil {
		req = &TemplateGetReq{}
	}

	var (
		data TemplateGetResp
		err  error
	)
	if data.response, err = c.apiClient.do(ctx, req, &data.Templates); err != nil {
		return &data, err
	}

	return &data, nil
}

// Exists executes a exists template request with the required TemplatExistsReq
// Deprecated: uses legacy API (/_template), correct API is /_index_template, use IndexTemplate.Exists instread
func (c templateClient) Exists(ctx context.Context, req TemplateExistsReq) (*opensearch.Response, error) {
	return c.apiClient.do(ctx, req, nil)
}

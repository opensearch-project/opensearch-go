// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"

	"github.com/opensearch-project/opensearch-go/v4"
)

type templateClient struct {
	apiClient *Client
}

// Create executes a creade template request with the required TemplateCreateReq
// Deprecated: uses legacy API (/_template), correct API is /_index_template, use IndexTemplate.Create instread
func (c templateClient) Create(ctx context.Context, req TemplateCreateReq) (*TemplateCreateResp, *opensearch.Response, error) {
	var data TemplateCreateResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Delete executes a delete template request with the required TemplateDeleteReq
// Deprecated: uses legacy API (/_template), correct API is /_index_template, use IndexTemplate.Delete instread
func (c templateClient) Delete(ctx context.Context, req TemplateDeleteReq) (*TemplateDeleteResp, *opensearch.Response, error) {
	var data TemplateDeleteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Get executes a get template request with the optional TemplateGetReq
// Deprecated: uses legacy API (/_template), correct API is /_index_template, use IndexTemplate.Get instread
func (c templateClient) Get(ctx context.Context, req *TemplateGetReq) (*TemplateGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &TemplateGetReq{}
	}

	var data TemplateGetResp

	resp, err := c.apiClient.do(ctx, req, &data.Templates)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Exists executes a exists template request with the required TemplatExistsReq
// Deprecated: uses legacy API (/_template), correct API is /_index_template, use IndexTemplate.Exists instread
func (c templateClient) Exists(ctx context.Context, req TemplateExistsReq) (*opensearch.Response, error) {
	return c.apiClient.do(ctx, req, nil)
}

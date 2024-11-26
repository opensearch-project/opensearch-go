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

type componentTemplateClient struct {
	apiClient *Client
}

// Create executes a creade componentTemplate request with the required ComponentTemplateCreateReq
func (c componentTemplateClient) Create(
	ctx context.Context,
	req ComponentTemplateCreateReq,
) (*ComponentTemplateCreateResp, *opensearch.Response, error) {
	var data ComponentTemplateCreateResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Delete executes a delete componentTemplate request with the required ComponentTemplateDeleteReq
func (c componentTemplateClient) Delete(
	ctx context.Context,
	req ComponentTemplateDeleteReq,
) (*ComponentTemplateDeleteResp, *opensearch.Response, error) {
	var data ComponentTemplateDeleteResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Get executes a get componentTemplate request with the optional ComponentTemplateGetReq
func (c componentTemplateClient) Get(
	ctx context.Context,
	req *ComponentTemplateGetReq,
) (*ComponentTemplateGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &ComponentTemplateGetReq{}
	}

	var data ComponentTemplateGetResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Exists executes a exists componentTemplate request with the required ComponentTemplatExistsReq
func (c componentTemplateClient) Exists(ctx context.Context, req ComponentTemplateExistsReq) (*opensearch.Response, error) {
	return c.apiClient.do(ctx, req, nil)
}

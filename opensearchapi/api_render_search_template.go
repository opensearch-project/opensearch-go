// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// RenderSearchTemplate executes a /_render/template request with the required RenderSearchTemplateReq
func (c Client) RenderSearchTemplate(
	ctx context.Context,
	req RenderSearchTemplateReq,
) (*RenderSearchTemplateResp, *opensearch.Response, error) {
	var data RenderSearchTemplateResp

	resp, err := c.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// RenderSearchTemplateReq represents possible options for the /_render/template request
type RenderSearchTemplateReq struct {
	TemplateID string

	Body io.Reader

	Header http.Header
	Params RenderSearchTemplateParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r RenderSearchTemplateReq) GetRequest() (*http.Request, error) {
	var path strings.Builder
	path.Grow(len("//_render/template") + len(r.TemplateID))
	path.WriteString("/_render/template")
	if len(r.TemplateID) > 0 {
		path.WriteString("/")
		path.WriteString(r.TemplateID)
	}
	return opensearch.BuildRequest(
		"POST",
		path.String(),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// RenderSearchTemplateResp represents the returned struct of the /_render/template response
type RenderSearchTemplateResp struct {
	TemplateOutput json.RawMessage `json:"template_output"`
}

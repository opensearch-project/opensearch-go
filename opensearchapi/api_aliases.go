// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"
	"io"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// Aliases executes an /_aliases request with the required AliasesReq
func (c Client) Aliases(ctx context.Context, req AliasesReq) (*AliasesResp, *opensearch.Response, error) {
	var data AliasesResp

	resp, err := c.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// AliasesReq represents possible options for the / request
type AliasesReq struct {
	Body io.Reader

	Header http.Header
	Params AliasesParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r AliasesReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"POST",
		"/_aliases",
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// AliasesResp represents the returned struct of the / response
type AliasesResp struct {
	Acknowledged bool `json:"acknowledged"`
}

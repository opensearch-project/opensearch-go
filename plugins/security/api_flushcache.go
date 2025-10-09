// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"context"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// FlushCache executes a flush cache request with the optional FlushCacheReq
func (c Client) FlushCache(ctx context.Context, req *FlushCacheReq) (FlushCacheResp, error) {
	if req == nil {
		req = &FlushCacheReq{}
	}

	var (
		data FlushCacheResp
		err  error
	)
	if data.response, err = c.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// FlushCacheReq represents possible options for the clush cache request
type FlushCacheReq struct {
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r FlushCacheReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"DELETE",
		"/_plugins/_security/api/cache",
		nil,
		make(map[string]string),
		r.Header,
	)
}

// FlushCacheResp represents the returned struct of the flush cache response
type FlushCacheResp struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r FlushCacheResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

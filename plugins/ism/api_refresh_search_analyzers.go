// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ism

import (
	"context"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// RefreshSearchAnalyzers executes a request to refresh search analyzers with the required RefreshSearchAnalyzersReq
func (c Client) RefreshSearchAnalyzers(ctx context.Context, req RefreshSearchAnalyzersReq) (RefreshSearchAnalyzersResp, error) {
	var (
		data RefreshSearchAnalyzersResp
		err  error
	)
	if data.response, err = c.do(ctx, req, &data); err != nil {
		return data, err
	}

	return data, nil
}

// RefreshSearchAnalyzersReq represents possible options for the /_plugins/_refresh_search_analyzers/<indices> request
type RefreshSearchAnalyzersReq struct {
	Indices []string

	Header http.Header
	Params RefreshSearchAnalyzersParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r RefreshSearchAnalyzersReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")

	var path strings.Builder
	path.Grow(len("/_plugins/_refresh_search_analyzers/") + len(indices))
	path.WriteString("_plugins/_refresh_search_analyzers")
	if len(r.Indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}

	return opensearch.BuildRequest(
		http.MethodPost,
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// RefreshSearchAnalyzersResp represents the returned struct of the refreshed search analyzers response
type RefreshSearchAnalyzersResp struct {
	Shards                   RefreshSearchAnalyzersShards                     `json:"_shards"`
	SuccessfulRefreshDetails []RefreshSearchAnalyzersSuccessfulRefreshDetails `json:"successful_refresh_details"`
	response                 *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r RefreshSearchAnalyzersResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// RefreshSearchAnalyzersShards is a subtype of RefreshSearchAnalyzersResp representing information about the updated shards
type RefreshSearchAnalyzersShards struct {
	Total      int `json:"total"`
	Successful int `json:"successful"`
	Failed     int `json:"failed"`
}

// RefreshSearchAnalyzersSuccessfulRefreshDetails is a subtype of RefreshSearchAnalyzersResp representing information about the analyzers
type RefreshSearchAnalyzersSuccessfulRefreshDetails struct {
	Index              string   `json:"index"`
	RefreshedAnalyzers []string `json:"refreshed_analyzers"`
}

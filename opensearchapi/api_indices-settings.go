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

type settingsClient struct {
	apiClient *Client
}

// Get executes a get settings request with the required SettingsGetReq
func (c settingsClient) Get(ctx context.Context, req *SettingsGetReq) (*SettingsGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &SettingsGetReq{}
	}

	var data SettingsGetResp

	resp, err := c.apiClient.do(ctx, req, &data.Indices)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// Put executes a put settings request with the required SettingsPutReq
func (c settingsClient) Put(ctx context.Context, req SettingsPutReq) (*SettingsPutResp, *opensearch.Response, error) {
	var data SettingsPutResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return nil, resp, err
	}

	return &data, resp, nil
}

// SettingsGetReq represents possible options for the settings get request
type SettingsGetReq struct {
	Indices  []string
	Settings []string

	Header http.Header
	Params SettingsGetParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r SettingsGetReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")
	settings := strings.Join(r.Settings, ",")

	var path strings.Builder
	path.Grow(11 + len(indices) + len(settings))
	if len(indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}
	path.WriteString("/_settings")
	if len(settings) > 0 {
		path.WriteString("/")
		path.WriteString(settings)
	}
	return opensearch.BuildRequest(
		"GET",
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// SettingsGetResp represents the returned struct of the settings get response
type SettingsGetResp struct {
	Indices map[string]struct {
		Settings json.RawMessage `json:"settings"`
	}
}

// SettingsPutReq represents possible options for the settings put request
type SettingsPutReq struct {
	Indices []string

	Body io.Reader

	Header http.Header
	Params SettingsPutParams
}

// GetRequest returns the *http.Request that gets executed by the client
func (r SettingsPutReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")

	var path strings.Builder
	path.Grow(10 + len(indices))
	path.WriteString("/")
	path.WriteString(indices)
	path.WriteString("/_settings")
	return opensearch.BuildRequest(
		"PUT",
		path.String(),
		r.Body,
		r.Params.get(),
		r.Header,
	)
}

// SettingsPutResp represents the returned struct of the settings put response
type SettingsPutResp struct {
	Acknowledged bool `json:"acknowledged"`
}

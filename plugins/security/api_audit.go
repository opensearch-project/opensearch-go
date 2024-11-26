// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"context"
	"encoding/json"
	"github.com/opensearch-project/opensearch-go/v4"
)

type auditClient struct {
	apiClient *Client
}

// Get executes a get audit request with the optional AuditGetReq
func (c auditClient) Get(ctx context.Context, req *AuditGetReq) (AuditGetResp, *opensearch.Response, error) {
	if req == nil {
		req = &AuditGetReq{}
	}

	var data AuditGetResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Put executes a put audit request with the required AuditPutReq
func (c auditClient) Put(ctx context.Context, req AuditPutReq) (AuditPutResp, *opensearch.Response, error) {
	var data AuditPutResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// Patch executes a patch audit request with the required AuditPatchReq
func (c auditClient) Patch(ctx context.Context, req AuditPatchReq) (AuditPatchResp, *opensearch.Response, error) {
	var data AuditPatchResp

	resp, err := c.apiClient.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// AuditConfig represents the security audit config uses for Put and Get request
type AuditConfig struct {
	Compliance struct {
		Enabled             bool            `json:"enabled"`
		WriteLogDiffs       bool            `json:"write_log_diffs"`
		ReadWatchedFields   json.RawMessage `json:"read_watched_fields"`
		ReadIgnoreUsers     []string        `json:"read_ignore_users"`
		WriteWatchedIndices []string        `json:"write_watched_indices"`
		WriteIgnoreUsers    []string        `json:"write_ignore_users"`
		ReadMetadataOnly    bool            `json:"read_metadata_only"`
		WriteMetadataOnly   bool            `json:"write_metadata_only"`
		ExternalConfig      bool            `json:"external_config"`
		InternalConfig      bool            `json:"internal_config"`
	} `json:"compliance"`
	Enabled bool `json:"enabled"`
	Audit   struct {
		IgnoreUsers    []string `json:"ignore_users"`
		IgnoreRequests []string `json:"ignore_requests"`
		// Needs to be a pointer so the omitempty machtes on nil and not on empty slice
		IgnoreHeaders               *[]string `json:"ignore_headers,omitempty"`
		IgnoreURLParams             *[]string `json:"ignore_url_params,omitempty"`
		DisabledRestCategories      []string  `json:"disabled_rest_categories"`
		DisabledTransportCategories []string  `json:"disabled_transport_categories"`
		LogRequestBody              bool      `json:"log_request_body"`
		ResolveIndices              bool      `json:"resolve_indices"`
		ResolveBulkRequests         bool      `json:"resolve_bulk_requests"`
		ExcludeSensitiveHeaders     bool      `json:"exclude_sensitive_headers"`
		EnableTransport             bool      `json:"enable_transport"`
		EnableRest                  bool      `json:"enable_rest"`
	} `json:"audit"`
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build !integration

package opensearchapi_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

func TestAliasPutReq_GetRequest_URLPath(t *testing.T) {
	tests := []struct {
		name     string
		req      opensearchapi.AliasPutReq
		wantPath string
	}{
		{
			name:     "with indices",
			req:      opensearchapi.AliasPutReq{Indices: []string{"myindex"}, Alias: "myalias"},
			wantPath: "/myindex/_alias/myalias",
		},
		{
			name:     "with multiple indices",
			req:      opensearchapi.AliasPutReq{Indices: []string{"idx1", "idx2"}, Alias: "myalias"},
			wantPath: "/idx1,idx2/_alias/myalias",
		},
		{
			name:     "without indices",
			req:      opensearchapi.AliasPutReq{Alias: "myalias"},
			wantPath: "/_alias/myalias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpReq, err := tt.req.GetRequest()
			require.NoError(t, err)
			require.NotNil(t, httpReq)
			assert.Equal(t, tt.wantPath, httpReq.URL.Path)
			assert.Empty(t, httpReq.URL.Host, "host should not be set from path parsing")
		})
	}
}

func TestAliasGetReq_GetRequest_URLPath(t *testing.T) {
	tests := []struct {
		name     string
		req      opensearchapi.AliasGetReq
		wantPath string
	}{
		{
			name:     "with indices and alias",
			req:      opensearchapi.AliasGetReq{Indices: []string{"myindex"}, Alias: []string{"myalias"}},
			wantPath: "/myindex/_alias/myalias",
		},
		{
			name:     "without indices",
			req:      opensearchapi.AliasGetReq{Alias: []string{"myalias"}},
			wantPath: "/_alias/myalias",
		},
		{
			name:     "without indices or alias",
			req:      opensearchapi.AliasGetReq{},
			wantPath: "/_alias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpReq, err := tt.req.GetRequest()
			require.NoError(t, err)
			require.NotNil(t, httpReq)
			assert.Equal(t, tt.wantPath, httpReq.URL.Path)
			assert.Empty(t, httpReq.URL.Host, "host should not be set from path parsing")
		})
	}
}

func TestAliasDeleteReq_GetRequest_URLPath(t *testing.T) {
	tests := []struct {
		name     string
		req      opensearchapi.AliasDeleteReq
		wantPath string
	}{
		{
			name:     "with indices and alias",
			req:      opensearchapi.AliasDeleteReq{Indices: []string{"myindex"}, Alias: []string{"myalias"}},
			wantPath: "/myindex/_alias/myalias",
		},
		{
			name:     "without indices",
			req:      opensearchapi.AliasDeleteReq{Alias: []string{"myalias"}},
			wantPath: "/_alias/myalias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpReq, err := tt.req.GetRequest()
			require.NoError(t, err)
			require.NotNil(t, httpReq)
			assert.Equal(t, tt.wantPath, httpReq.URL.Path)
			assert.Empty(t, httpReq.URL.Host, "host should not be set from path parsing")
		})
	}
}

func TestAliasExistsReq_GetRequest_URLPath(t *testing.T) {
	tests := []struct {
		name     string
		req      opensearchapi.AliasExistsReq
		wantPath string
	}{
		{
			name:     "with indices and alias",
			req:      opensearchapi.AliasExistsReq{Indices: []string{"myindex"}, Alias: []string{"myalias"}},
			wantPath: "/myindex/_alias/myalias",
		},
		{
			name:     "without indices",
			req:      opensearchapi.AliasExistsReq{Alias: []string{"myalias"}},
			wantPath: "/_alias/myalias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpReq, err := tt.req.GetRequest()
			require.NoError(t, err)
			require.NotNil(t, httpReq)
			assert.Equal(t, tt.wantPath, httpReq.URL.Path)
			assert.Empty(t, httpReq.URL.Host, "host should not be set from path parsing")
		})
	}
}

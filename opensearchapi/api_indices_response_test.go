// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build !integration

package opensearchapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

func TestMappingGetResp_GetIndices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		json       string
		wantNil    bool
		wantLen    int
		wantKeys   []string
		checkField func(t *testing.T, indices map[string]opensearchapi.MappingGetRespIndex)
	}{
		{
			name:    "uninitialized response",
			wantNil: true,
		},
		{
			name: "two indices",
			json: `{
				"test-index-1": {"mappings": {"properties": {"field1": {"type": "text"}}}},
				"test-index-2": {"mappings": {"properties": {"field2": {"type": "keyword"}}}}
			}`,
			wantLen:  2,
			wantKeys: []string{"test-index-1", "test-index-2"},
			checkField: func(t *testing.T, indices map[string]opensearchapi.MappingGetRespIndex) {
				require.Contains(t, string(indices["test-index-1"].Mappings), "field1")
			},
		},
		{
			name:     "single index",
			json:     `{"my-index": {"mappings": {}}}`,
			wantLen:  1,
			wantKeys: []string{"my-index"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var resp opensearchapi.MappingGetResp
			if tt.json != "" {
				require.NoError(t, json.Unmarshal([]byte(tt.json), &resp))
			}
			indices := resp.GetIndices()
			if tt.wantNil {
				require.Nil(t, indices)
				return
			}
			require.NotNil(t, indices)
			require.Len(t, indices, tt.wantLen)
			for _, k := range tt.wantKeys {
				require.Contains(t, indices, k)
			}
			if tt.checkField != nil {
				tt.checkField(t, indices)
			}
		})
	}
}

func TestSettingsGetResp_GetIndices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		json       string
		wantNil    bool
		wantLen    int
		wantKeys   []string
		checkField func(t *testing.T, indices map[string]opensearchapi.SettingsGetRespIndex)
	}{
		{
			name:    "uninitialized response",
			wantNil: true,
		},
		{
			name: "two indices",
			json: `{
				"test-index-1": {"settings": {"index": {"number_of_shards": "1", "number_of_replicas": "0"}}},
				"test-index-2": {"settings": {"index": {"number_of_shards": "2", "number_of_replicas": "1"}}}
			}`,
			wantLen:  2,
			wantKeys: []string{"test-index-1", "test-index-2"},
			checkField: func(t *testing.T, indices map[string]opensearchapi.SettingsGetRespIndex) {
				require.Contains(t, string(indices["test-index-1"].Settings), "number_of_shards")
			},
		},
		{
			name:     "single index",
			json:     `{"my-index": {"settings": {}}}`,
			wantLen:  1,
			wantKeys: []string{"my-index"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var resp opensearchapi.SettingsGetResp
			if tt.json != "" {
				require.NoError(t, json.Unmarshal([]byte(tt.json), &resp))
			}
			indices := resp.GetIndices()
			if tt.wantNil {
				require.Nil(t, indices)
				return
			}
			require.NotNil(t, indices)
			require.Len(t, indices, tt.wantLen)
			for _, k := range tt.wantKeys {
				require.Contains(t, indices, k)
			}
			if tt.checkField != nil {
				tt.checkField(t, indices)
			}
		})
	}
}

func TestAliasGetResp_GetIndices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		json       string
		wantNil    bool
		wantLen    int
		wantKeys   []string
		checkField func(t *testing.T, indices map[string]opensearchapi.AliasGetRespIndex)
	}{
		{
			name:    "uninitialized response",
			wantNil: true,
		},
		{
			name: "two indices with aliases",
			json: `{
				"test-index-1": {"aliases": {"alias1": {}, "alias2": {"filter": {"term": {"user": "kimchy"}}}}},
				"test-index-2": {"aliases": {"alias3": {}}}
			}`,
			wantLen:  2,
			wantKeys: []string{"test-index-1", "test-index-2"},
			checkField: func(t *testing.T, indices map[string]opensearchapi.AliasGetRespIndex) {
				require.Len(t, indices["test-index-1"].Aliases, 2)
				require.Contains(t, indices["test-index-1"].Aliases, "alias1")
				require.Contains(t, indices["test-index-1"].Aliases, "alias2")
			},
		},
		{
			name:     "index with empty aliases",
			json:     `{"my-index": {"aliases": {}}}`,
			wantLen:  1,
			wantKeys: []string{"my-index"},
			checkField: func(t *testing.T, indices map[string]opensearchapi.AliasGetRespIndex) {
				require.Empty(t, indices["my-index"].Aliases)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var resp opensearchapi.AliasGetResp
			if tt.json != "" {
				require.NoError(t, json.Unmarshal([]byte(tt.json), &resp))
			}
			indices := resp.GetIndices()
			if tt.wantNil {
				require.Nil(t, indices)
				return
			}
			require.NotNil(t, indices)
			require.Len(t, indices, tt.wantLen)
			for _, k := range tt.wantKeys {
				require.Contains(t, indices, k)
			}
			if tt.checkField != nil {
				tt.checkField(t, indices)
			}
		})
	}
}

func TestIndicesRecoveryResp_GetIndices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		json       string
		wantNil    bool
		wantLen    int
		wantKeys   []string
		checkField func(t *testing.T, indices map[string]opensearchapi.IndicesRecoveryRespIndex)
	}{
		{
			name:    "uninitialized response",
			wantNil: true,
		},
		{
			name: "two indices",
			json: `{
				"test-index-1": {"shards": [{"id": 0, "type": "STORE", "stage": "DONE", "primary": true, "start_time_in_millis": 0, "stop_time_in_millis": 0, "total_time_in_millis": 10, "source": {"id": "n1", "host": "127.0.0.1", "transport_address": "127.0.0.1:9300", "ip": "127.0.0.1", "name": "node-1"}, "target": {"id": "n1", "host": "127.0.0.1", "transport_address": "127.0.0.1:9300", "ip": "127.0.0.1", "name": "node-1"}, "index": {"size": {"total_in_bytes": 1000, "reused_in_bytes": 900, "recovered_in_bytes": 100, "percent": "100.0%"}, "files": {"total": 10, "reused": 9, "recovered": 1, "percent": "100.0%"}, "total_time_in_millis": 5, "source_throttle_time_in_millis": 0, "target_throttle_time_in_millis": 0}, "translog": {"recovered": 0, "total": 0, "percent": "100.0%", "total_on_start": 0, "total_time_in_millis": 0}, "verify_index": {"check_index_time_in_millis": 0, "total_time_in_millis": 0}}]},
				"test-index-2": {"shards": [{"id": 0, "type": "PEER", "stage": "DONE", "primary": false, "start_time_in_millis": 0, "stop_time_in_millis": 0, "total_time_in_millis": 60, "source": {"id": "n1", "host": "127.0.0.1", "transport_address": "127.0.0.1:9300", "ip": "127.0.0.1", "name": "node-1"}, "target": {"id": "n2", "host": "127.0.0.2", "transport_address": "127.0.0.2:9300", "ip": "127.0.0.2", "name": "node-2"}, "index": {"size": {"total_in_bytes": 2000, "reused_in_bytes": 0, "recovered_in_bytes": 2000, "percent": "100.0%"}, "files": {"total": 15, "reused": 0, "recovered": 15, "percent": "100.0%"}, "total_time_in_millis": 50, "source_throttle_time_in_millis": 10, "target_throttle_time_in_millis": 5}, "translog": {"recovered": 100, "total": 100, "percent": "100.0%", "total_on_start": 100, "total_time_in_millis": 5}, "verify_index": {"check_index_time_in_millis": 2, "total_time_in_millis": 2}}]}
			}`,
			wantLen:  2,
			wantKeys: []string{"test-index-1", "test-index-2"},
			checkField: func(t *testing.T, indices map[string]opensearchapi.IndicesRecoveryRespIndex) {
				require.Len(t, indices["test-index-1"].Shards, 1)
				require.Equal(t, "STORE", indices["test-index-1"].Shards[0].Type)
				require.True(t, indices["test-index-1"].Shards[0].Primary)
				require.Equal(t, "PEER", indices["test-index-2"].Shards[0].Type)
				require.False(t, indices["test-index-2"].Shards[0].Primary)
			},
		},
		{
			name: "single index with multiple shards",
			json: `{
				"my-index": {"shards": [
					{"id": 0, "type": "STORE", "stage": "DONE", "primary": true, "start_time_in_millis": 0, "stop_time_in_millis": 0, "total_time_in_millis": 10, "source": {"id": "n1", "host": "127.0.0.1", "transport_address": "127.0.0.1:9300", "ip": "127.0.0.1", "name": "node-1"}, "target": {"id": "n1", "host": "127.0.0.1", "transport_address": "127.0.0.1:9300", "ip": "127.0.0.1", "name": "node-1"}, "index": {"size": {"total_in_bytes": 1000, "reused_in_bytes": 900, "recovered_in_bytes": 100, "percent": "100.0%"}, "files": {"total": 10, "reused": 9, "recovered": 1, "percent": "100.0%"}, "total_time_in_millis": 5, "source_throttle_time_in_millis": 0, "target_throttle_time_in_millis": 0}, "translog": {"recovered": 0, "total": 0, "percent": "100.0%", "total_on_start": 0, "total_time_in_millis": 0}, "verify_index": {"check_index_time_in_millis": 0, "total_time_in_millis": 0}},
					{"id": 1, "type": "STORE", "stage": "DONE", "primary": true, "start_time_in_millis": 0, "stop_time_in_millis": 0, "total_time_in_millis": 10, "source": {"id": "n1", "host": "127.0.0.1", "transport_address": "127.0.0.1:9300", "ip": "127.0.0.1", "name": "node-1"}, "target": {"id": "n1", "host": "127.0.0.1", "transport_address": "127.0.0.1:9300", "ip": "127.0.0.1", "name": "node-1"}, "index": {"size": {"total_in_bytes": 1000, "reused_in_bytes": 900, "recovered_in_bytes": 100, "percent": "100.0%"}, "files": {"total": 10, "reused": 9, "recovered": 1, "percent": "100.0%"}, "total_time_in_millis": 5, "source_throttle_time_in_millis": 0, "target_throttle_time_in_millis": 0}, "translog": {"recovered": 0, "total": 0, "percent": "100.0%", "total_on_start": 0, "total_time_in_millis": 0}, "verify_index": {"check_index_time_in_millis": 0, "total_time_in_millis": 0}}
				]}
			}`,
			wantLen:  1,
			wantKeys: []string{"my-index"},
			checkField: func(t *testing.T, indices map[string]opensearchapi.IndicesRecoveryRespIndex) {
				require.Len(t, indices["my-index"].Shards, 2)
				require.Equal(t, 0, indices["my-index"].Shards[0].ID)
				require.Equal(t, 1, indices["my-index"].Shards[1].ID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var resp opensearchapi.IndicesRecoveryResp
			if tt.json != "" {
				require.NoError(t, json.Unmarshal([]byte(tt.json), &resp))
			}
			indices := resp.GetIndices()
			if tt.wantNil {
				require.Nil(t, indices)
				return
			}
			require.NotNil(t, indices)
			require.Len(t, indices, tt.wantLen)
			for _, k := range tt.wantKeys {
				require.Contains(t, indices, k)
			}
			if tt.checkField != nil {
				tt.checkField(t, indices)
			}
		})
	}
}

func TestResp_Inspect(t *testing.T) {
	t.Parallel()

	type inspectable interface {
		Inspect() opensearchapi.Inspect
	}

	tests := []struct {
		name string
		resp inspectable
	}{
		{name: "MappingFieldResp", resp: opensearchapi.MappingFieldResp{}},
		{name: "MappingGetResp", resp: opensearchapi.MappingGetResp{}},
		{name: "MappingPutResp", resp: opensearchapi.MappingPutResp{}},
		{name: "SettingsGetResp", resp: opensearchapi.SettingsGetResp{}},
		{name: "SettingsPutResp", resp: opensearchapi.SettingsPutResp{}},
		{name: "AliasGetResp", resp: opensearchapi.AliasGetResp{}},
		{name: "AliasDeleteResp", resp: opensearchapi.AliasDeleteResp{}},
		{name: "AliasPutResp", resp: opensearchapi.AliasPutResp{}},
		{name: "IndicesRecoveryResp", resp: opensearchapi.IndicesRecoveryResp{}},
		{name: "IndicesDeleteResp", resp: opensearchapi.IndicesDeleteResp{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			inspect := tt.resp.Inspect()
			require.Nil(t, inspect.Response)
		})
	}
}

func TestMappingFieldReq_GetRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		req        opensearchapi.MappingFieldReq
		wantMethod string
		wantPath   string
		wantErr    bool
	}{
		{
			name:       "with valid indices and fields",
			req:        opensearchapi.MappingFieldReq{Indices: []string{"test-index"}, Fields: []string{"test_field"}},
			wantMethod: http.MethodGet,
			wantPath:   "/test-index/_mapping/field/test_field",
		},
		{
			name:       "with wildcard field",
			req:        opensearchapi.MappingFieldReq{Indices: []string{"test-index"}, Fields: []string{"*"}},
			wantMethod: http.MethodGet,
			wantPath:   "/test-index/_mapping/field/*",
		},
		{
			name:    "with empty fields returns error",
			req:     opensearchapi.MappingFieldReq{Indices: []string{"test-index"}, Fields: []string{}},
			wantMethod: http.MethodGet,
			wantErr: true,
		},
		{
			name:    "with nil fields returns error",
			req:     opensearchapi.MappingFieldReq{Indices: []string{"test-index"}},
			wantMethod: http.MethodGet,
			wantErr: true,
		},
		{
			name:       "without indices",
			req:        opensearchapi.MappingFieldReq{Fields: []string{"test_field"}},
			wantMethod: http.MethodGet,
			wantPath:   "/_mapping/field/test_field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			httpReq, err := tt.req.GetRequest(tt.wantMethod)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantMethod, httpReq.Method)
			require.Equal(t, tt.wantPath, httpReq.URL.Path)
		})
	}
}

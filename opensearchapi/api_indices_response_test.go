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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

// TestMappingGetResp_GetIndices tests the GetIndices method for MappingGetResp
func TestMappingGetResp_GetIndices(t *testing.T) {
	t.Run("returns empty map for uninitialized response", func(t *testing.T) {
		resp := &opensearchapi.MappingGetResp{}
		indices := resp.GetIndices()
		assert.Nil(t, indices)
	})

	t.Run("returns correct map after unmarshaling", func(t *testing.T) {
		jsonData := `{
			"test-index-1": {
				"mappings": {
					"properties": {
						"field1": {"type": "text"}
					}
				}
			},
			"test-index-2": {
				"mappings": {
					"properties": {
						"field2": {"type": "keyword"}
					}
				}
			}
		}`

		var resp opensearchapi.MappingGetResp
		err := json.Unmarshal([]byte(jsonData), &resp)
		require.NoError(t, err)

		indices := resp.GetIndices()
		require.NotNil(t, indices)
		assert.Len(t, indices, 2)
		assert.Contains(t, indices, "test-index-1")
		assert.Contains(t, indices, "test-index-2")

		// Verify mappings are preserved as RawMessage
		index1 := indices["test-index-1"]
		assert.NotNil(t, index1.Mappings)
		assert.Contains(t, string(index1.Mappings), "field1")
	})

	t.Run("returns single index", func(t *testing.T) {
		jsonData := `{
			"my-index": {
				"mappings": {}
			}
		}`

		var resp opensearchapi.MappingGetResp
		err := json.Unmarshal([]byte(jsonData), &resp)
		require.NoError(t, err)

		indices := resp.GetIndices()
		require.NotNil(t, indices)
		assert.Len(t, indices, 1)
		assert.Contains(t, indices, "my-index")
	})
}

// TestSettingsGetResp_GetIndices tests the GetIndices method for SettingsGetResp
func TestSettingsGetResp_GetIndices(t *testing.T) {
	t.Run("returns empty map for uninitialized response", func(t *testing.T) {
		resp := &opensearchapi.SettingsGetResp{}
		indices := resp.GetIndices()
		assert.Nil(t, indices)
	})

	t.Run("returns correct map after unmarshaling", func(t *testing.T) {
		jsonData := `{
			"test-index-1": {
				"settings": {
					"index": {
						"number_of_shards": "1",
						"number_of_replicas": "0"
					}
				}
			},
			"test-index-2": {
				"settings": {
					"index": {
						"number_of_shards": "2",
						"number_of_replicas": "1"
					}
				}
			}
		}`

		var resp opensearchapi.SettingsGetResp
		err := json.Unmarshal([]byte(jsonData), &resp)
		require.NoError(t, err)

		indices := resp.GetIndices()
		require.NotNil(t, indices)
		assert.Len(t, indices, 2)
		assert.Contains(t, indices, "test-index-1")
		assert.Contains(t, indices, "test-index-2")

		// Verify settings are preserved as RawMessage
		index1 := indices["test-index-1"]
		assert.NotNil(t, index1.Settings)
		assert.Contains(t, string(index1.Settings), "number_of_shards")
	})

	t.Run("returns single index", func(t *testing.T) {
		jsonData := `{
			"my-index": {
				"settings": {}
			}
		}`

		var resp opensearchapi.SettingsGetResp
		err := json.Unmarshal([]byte(jsonData), &resp)
		require.NoError(t, err)

		indices := resp.GetIndices()
		require.NotNil(t, indices)
		assert.Len(t, indices, 1)
		assert.Contains(t, indices, "my-index")
	})
}

// TestAliasGetResp_GetIndices tests the GetIndices method for AliasGetResp
func TestAliasGetResp_GetIndices(t *testing.T) {
	t.Run("returns empty map for uninitialized response", func(t *testing.T) {
		resp := &opensearchapi.AliasGetResp{}
		indices := resp.GetIndices()
		assert.Nil(t, indices)
	})

	t.Run("returns correct map after unmarshaling", func(t *testing.T) {
		jsonData := `{
			"test-index-1": {
				"aliases": {
					"alias1": {},
					"alias2": {"filter": {"term": {"user": "kimchy"}}}
				}
			},
			"test-index-2": {
				"aliases": {
					"alias3": {}
				}
			}
		}`

		var resp opensearchapi.AliasGetResp
		err := json.Unmarshal([]byte(jsonData), &resp)
		require.NoError(t, err)

		indices := resp.GetIndices()
		require.NotNil(t, indices)
		assert.Len(t, indices, 2)
		assert.Contains(t, indices, "test-index-1")
		assert.Contains(t, indices, "test-index-2")

		// Verify aliases are preserved
		index1 := indices["test-index-1"]
		assert.NotNil(t, index1.Aliases)
		assert.Len(t, index1.Aliases, 2)
		assert.Contains(t, index1.Aliases, "alias1")
		assert.Contains(t, index1.Aliases, "alias2")
	})

	t.Run("returns index with empty aliases", func(t *testing.T) {
		jsonData := `{
			"my-index": {
				"aliases": {}
			}
		}`

		var resp opensearchapi.AliasGetResp
		err := json.Unmarshal([]byte(jsonData), &resp)
		require.NoError(t, err)

		indices := resp.GetIndices()
		require.NotNil(t, indices)
		assert.Len(t, indices, 1)
		assert.Contains(t, indices, "my-index")
		assert.Empty(t, indices["my-index"].Aliases)
	})
}

// TestIndicesRecoveryResp_GetIndices tests the GetIndices method for IndicesRecoveryResp
func TestIndicesRecoveryResp_GetIndices(t *testing.T) {
	t.Run("returns empty map for uninitialized response", func(t *testing.T) {
		resp := &opensearchapi.IndicesRecoveryResp{}
		indices := resp.GetIndices()
		assert.Nil(t, indices)
	})

	t.Run("returns correct map after unmarshaling", func(t *testing.T) {
		jsonData := `{
			"test-index-1": {
				"shards": [
					{
						"id": 0,
						"type": "STORE",
						"stage": "DONE",
						"primary": true,
						"start_time_in_millis": 1234567890,
						"stop_time_in_millis": 1234567900,
						"total_time_in_millis": 10,
						"source": {
							"id": "node1",
							"host": "127.0.0.1",
							"transport_address": "127.0.0.1:9300",
							"ip": "127.0.0.1",
							"name": "node-1"
						},
						"target": {
							"id": "node1",
							"host": "127.0.0.1",
							"transport_address": "127.0.0.1:9300",
							"ip": "127.0.0.1",
							"name": "node-1"
						},
						"index": {
							"size": {
								"total_in_bytes": 1000,
								"reused_in_bytes": 900,
								"recovered_in_bytes": 100,
								"percent": "100.0%"
							},
							"files": {
								"total": 10,
								"reused": 9,
								"recovered": 1,
								"percent": "100.0%"
							},
							"total_time_in_millis": 5,
							"source_throttle_time_in_millis": 0,
							"target_throttle_time_in_millis": 0
						},
						"translog": {
							"recovered": 0,
							"total": 0,
							"percent": "100.0%",
							"total_on_start": 0,
							"total_time_in_millis": 0
						},
						"verify_index": {
							"check_index_time_in_millis": 0,
							"total_time_in_millis": 0
						}
					}
				]
			},
			"test-index-2": {
				"shards": [
					{
						"id": 0,
						"type": "PEER",
						"stage": "DONE",
						"primary": false,
						"start_time_in_millis": 1234567890,
						"stop_time_in_millis": 1234567950,
						"total_time_in_millis": 60,
						"source": {
							"id": "node1",
							"host": "127.0.0.1",
							"transport_address": "127.0.0.1:9300",
							"ip": "127.0.0.1",
							"name": "node-1"
						},
						"target": {
							"id": "node2",
							"host": "127.0.0.2",
							"transport_address": "127.0.0.2:9300",
							"ip": "127.0.0.2",
							"name": "node-2"
						},
						"index": {
							"size": {
								"total_in_bytes": 2000,
								"reused_in_bytes": 0,
								"recovered_in_bytes": 2000,
								"percent": "100.0%"
							},
							"files": {
								"total": 15,
								"reused": 0,
								"recovered": 15,
								"percent": "100.0%"
							},
							"total_time_in_millis": 50,
							"source_throttle_time_in_millis": 10,
							"target_throttle_time_in_millis": 5
						},
						"translog": {
							"recovered": 100,
							"total": 100,
							"percent": "100.0%",
							"total_on_start": 100,
							"total_time_in_millis": 5
						},
						"verify_index": {
							"check_index_time_in_millis": 2,
							"total_time_in_millis": 2
						}
					}
				]
			}
		}`

		var resp opensearchapi.IndicesRecoveryResp
		err := json.Unmarshal([]byte(jsonData), &resp)
		require.NoError(t, err)

		indices := resp.GetIndices()
		require.NotNil(t, indices)
		assert.Len(t, indices, 2)
		assert.Contains(t, indices, "test-index-1")
		assert.Contains(t, indices, "test-index-2")

		// Verify recovery data structure
		index1 := indices["test-index-1"]
		require.Len(t, index1.Shards, 1)
		assert.Equal(t, 0, index1.Shards[0].ID)
		assert.Equal(t, "STORE", index1.Shards[0].Type)
		assert.Equal(t, "DONE", index1.Shards[0].Stage)
		assert.True(t, index1.Shards[0].Primary)
		assert.Equal(t, "node-1", index1.Shards[0].Source.Name)
		assert.Equal(t, "node-1", index1.Shards[0].Target.Name)

		index2 := indices["test-index-2"]
		require.Len(t, index2.Shards, 1)
		assert.Equal(t, "PEER", index2.Shards[0].Type)
		assert.False(t, index2.Shards[0].Primary)
		assert.Equal(t, "node-1", index2.Shards[0].Source.Name)
		assert.Equal(t, "node-2", index2.Shards[0].Target.Name)
	})

	t.Run("returns single index with multiple shards", func(t *testing.T) {
		jsonData := `{
			"my-index": {
				"shards": [
					{
						"id": 0,
						"type": "STORE",
						"stage": "DONE",
						"primary": true,
						"start_time_in_millis": 1234567890,
						"stop_time_in_millis": 1234567900,
						"total_time_in_millis": 10,
						"source": {
							"id": "node1",
							"host": "127.0.0.1",
							"transport_address": "127.0.0.1:9300",
							"ip": "127.0.0.1",
							"name": "node-1"
						},
						"target": {
							"id": "node1",
							"host": "127.0.0.1",
							"transport_address": "127.0.0.1:9300",
							"ip": "127.0.0.1",
							"name": "node-1"
						},
						"index": {
							"size": {
								"total_in_bytes": 1000,
								"reused_in_bytes": 900,
								"recovered_in_bytes": 100,
								"percent": "100.0%"
							},
							"files": {
								"total": 10,
								"reused": 9,
								"recovered": 1,
								"percent": "100.0%"
							},
							"total_time_in_millis": 5,
							"source_throttle_time_in_millis": 0,
							"target_throttle_time_in_millis": 0
						},
						"translog": {
							"recovered": 0,
							"total": 0,
							"percent": "100.0%",
							"total_on_start": 0,
							"total_time_in_millis": 0
						},
						"verify_index": {
							"check_index_time_in_millis": 0,
							"total_time_in_millis": 0
						}
					},
					{
						"id": 1,
						"type": "STORE",
						"stage": "DONE",
						"primary": true,
						"start_time_in_millis": 1234567890,
						"stop_time_in_millis": 1234567900,
						"total_time_in_millis": 10,
						"source": {
							"id": "node1",
							"host": "127.0.0.1",
							"transport_address": "127.0.0.1:9300",
							"ip": "127.0.0.1",
							"name": "node-1"
						},
						"target": {
							"id": "node1",
							"host": "127.0.0.1",
							"transport_address": "127.0.0.1:9300",
							"ip": "127.0.0.1",
							"name": "node-1"
						},
						"index": {
							"size": {
								"total_in_bytes": 1000,
								"reused_in_bytes": 900,
								"recovered_in_bytes": 100,
								"percent": "100.0%"
							},
							"files": {
								"total": 10,
								"reused": 9,
								"recovered": 1,
								"percent": "100.0%"
							},
							"total_time_in_millis": 5,
							"source_throttle_time_in_millis": 0,
							"target_throttle_time_in_millis": 0
						},
						"translog": {
							"recovered": 0,
							"total": 0,
							"percent": "100.0%",
							"total_on_start": 0,
							"total_time_in_millis": 0
						},
						"verify_index": {
							"check_index_time_in_millis": 0,
							"total_time_in_millis": 0
						}
					}
				]
			}
		}`

		var resp opensearchapi.IndicesRecoveryResp
		err := json.Unmarshal([]byte(jsonData), &resp)
		require.NoError(t, err)

		indices := resp.GetIndices()
		require.NotNil(t, indices)
		assert.Len(t, indices, 1)
		assert.Contains(t, indices, "my-index")

		// Verify multiple shards
		myIndex := indices["my-index"]
		require.Len(t, myIndex.Shards, 2)
		assert.Equal(t, 0, myIndex.Shards[0].ID)
		assert.Equal(t, 1, myIndex.Shards[1].ID)
	})
}

// TestMappingFieldResp_Inspect tests the Inspect method for MappingFieldResp
func TestMappingFieldResp_Inspect(t *testing.T) {
	resp := opensearchapi.MappingFieldResp{}
	inspect := resp.Inspect()
	assert.Nil(t, inspect.Response)
}

// TestMappingGetResp_Inspect tests the Inspect method for MappingGetResp
func TestMappingGetResp_Inspect(t *testing.T) {
	resp := opensearchapi.MappingGetResp{}
	inspect := resp.Inspect()
	assert.Nil(t, inspect.Response)
}

// TestMappingPutResp_Inspect tests the Inspect method for MappingPutResp
func TestMappingPutResp_Inspect(t *testing.T) {
	resp := opensearchapi.MappingPutResp{}
	inspect := resp.Inspect()
	assert.Nil(t, inspect.Response)
}

// TestSettingsGetResp_Inspect tests the Inspect method for SettingsGetResp
func TestSettingsGetResp_Inspect(t *testing.T) {
	resp := opensearchapi.SettingsGetResp{}
	inspect := resp.Inspect()
	assert.Nil(t, inspect.Response)
}

// TestSettingsPutResp_Inspect tests the Inspect method for SettingsPutResp
func TestSettingsPutResp_Inspect(t *testing.T) {
	resp := opensearchapi.SettingsPutResp{}
	inspect := resp.Inspect()
	assert.Nil(t, inspect.Response)
}

// TestAliasGetResp_Inspect tests the Inspect method for AliasGetResp
func TestAliasGetResp_Inspect(t *testing.T) {
	resp := opensearchapi.AliasGetResp{}
	inspect := resp.Inspect()
	assert.Nil(t, inspect.Response)
}

// TestAliasDeleteResp_Inspect tests the Inspect method for AliasDeleteResp
func TestAliasDeleteResp_Inspect(t *testing.T) {
	resp := opensearchapi.AliasDeleteResp{}
	inspect := resp.Inspect()
	assert.Nil(t, inspect.Response)
}

// TestAliasPutResp_Inspect tests the Inspect method for AliasPutResp
func TestAliasPutResp_Inspect(t *testing.T) {
	resp := opensearchapi.AliasPutResp{}
	inspect := resp.Inspect()
	assert.Nil(t, inspect.Response)
}

// TestIndicesRecoveryResp_Inspect tests the Inspect method for IndicesRecoveryResp
func TestIndicesRecoveryResp_Inspect(t *testing.T) {
	resp := opensearchapi.IndicesRecoveryResp{}
	inspect := resp.Inspect()
	assert.Nil(t, inspect.Response)
}

// TestIndicesDeleteResp_Inspect tests the Inspect method for IndicesDeleteResp
func TestIndicesDeleteResp_Inspect(t *testing.T) {
	resp := opensearchapi.IndicesDeleteResp{}
	inspect := resp.Inspect()
	assert.Nil(t, inspect.Response)
}

// TestMappingFieldReq_GetRequest tests the GetRequest method for MappingFieldReq
func TestMappingFieldReq_GetRequest(t *testing.T) {
	t.Run("with valid indices and fields", func(t *testing.T) {
		req := opensearchapi.MappingFieldReq{
			Indices: []string{"test-index"},
			Fields:  []string{"test_field"},
		}
		httpReq, err := req.GetRequest()
		require.NoError(t, err)
		assert.Equal(t, "GET", httpReq.Method)
		assert.Equal(t, "/test-index/_mapping/field/test_field", httpReq.URL.Path)
	})

	t.Run("with wildcard field", func(t *testing.T) {
		req := opensearchapi.MappingFieldReq{
			Indices: []string{"test-index"},
			Fields:  []string{"*"},
		}
		httpReq, err := req.GetRequest()
		require.NoError(t, err)
		assert.Equal(t, "GET", httpReq.Method)
		assert.Equal(t, "/test-index/_mapping/field/*", httpReq.URL.Path)
	})

	t.Run("with empty fields creates invalid path", func(t *testing.T) {
		req := opensearchapi.MappingFieldReq{
			Indices: []string{"test-index"},
			Fields:  []string{},
		}
		httpReq, err := req.GetRequest()
		require.NoError(t, err)
		// Empty fields results in path ending with slash - invalid OpenSearch API call
		assert.Equal(t, "/test-index/_mapping/field/", httpReq.URL.Path)
	})

	t.Run("with nil fields creates invalid path", func(t *testing.T) {
		req := opensearchapi.MappingFieldReq{
			Indices: []string{"test-index"},
			Fields:  nil,
		}
		httpReq, err := req.GetRequest()
		require.NoError(t, err)
		// Nil fields results in path ending with slash - invalid OpenSearch API call
		assert.Equal(t, "/test-index/_mapping/field/", httpReq.URL.Path)
	})

	t.Run("without indices", func(t *testing.T) {
		req := opensearchapi.MappingFieldReq{
			Fields: []string{"test_field"},
		}
		httpReq, err := req.GetRequest()
		require.NoError(t, err)
		assert.Equal(t, "GET", httpReq.Method)
		// Without indices, queries all indices
		assert.Equal(t, "/_mapping/field/test_field", httpReq.URL.Path)
	})
}

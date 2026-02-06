// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
)

// getClusterSize detects the number of nodes in the cluster for adaptive test behavior
func getClusterSize(t *testing.T, client *opensearchapi.Client) int {
	t.Helper()

	resp, err := client.Cluster.Health(nil, nil)
	if err != nil {
		t.Logf("Could not detect cluster size, assuming single node: %v", err)
		return 1
	}

	nodeCount := resp.NumberOfNodes
	if testutil.IsDebugEnabled(t) {
		t.Logf("Detected %d node(s) in cluster", nodeCount)
	}
	return nodeCount
}

func TestIndicesClientNew(t *testing.T) {
	client, err := ostest.NewClient(t)
	require.Nil(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	// Detect cluster size for adaptive test behavior
	clusterSize := getClusterSize(t, client)
	isSingleNode := clusterSize == 1

	// Standard validation functions
	validateDefault := func(t *testing.T, res osapitest.Response, err error) {
		require.Nil(t, err)
		require.NotNil(t, res)
		require.NotNil(t, res.Inspect().Response)
		ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
	}

	// Factory function for validating APIs with dynamic index names as top-level keys
	validateDynamicIndexResponse := func(expectedIndexPrefix string, requiredFields ...string) func(*testing.T, osapitest.Response, error) {
		return func(t *testing.T, res osapitest.Response, err error) {
			require.Nil(t, err)
			require.NotNil(t, res)
			require.NotNil(t, res.Inspect().Response)

			// Validate HTTP success
			response := res.Inspect().Response
			require.True(t, response.StatusCode >= 200 && response.StatusCode < 300,
				"Expected successful HTTP status, got %d", response.StatusCode)

			// Parse response body to validate structure
			body := res.Inspect().Response.Body
			bodyBytes, err := io.ReadAll(body)
			require.Nil(t, err, "Failed to read response body")

			var responseData map[string]interface{}
			require.Nil(t, json.Unmarshal(bodyBytes, &responseData))

			// Find index with matching prefix
			var foundIndex string
			var indexData map[string]interface{}
			for indexName, data := range responseData {
				if strings.HasPrefix(indexName, expectedIndexPrefix) {
					foundIndex = indexName
					var ok bool
					indexData, ok = data.(map[string]interface{})
					require.True(t, ok, "Index data should be an object, got %T", data)
					break
				}
			}

			require.NotEmpty(t, foundIndex, "Expected to find index with prefix %q in response", expectedIndexPrefix)
			require.NotNil(t, indexData, "Index data should not be nil")

			// Validate required fields exist in index data
			for _, field := range requiredFields {
				require.Contains(t, indexData, field, "Index data should contain field %q", field)
			}
		}
	}

	// Validation function for APIs with dynamic index names as top-level keys
	// These APIs can't use strict JSON comparison due to dynamic structure
	validateDynamicResponse := func(t *testing.T, res osapitest.Response, err error) {
		require.Nil(t, err)
		require.NotNil(t, res)
		require.NotNil(t, res.Inspect().Response)
		// Skip ostest.CompareRawJSONwithParsedJSON for dynamic structure APIs
		// Instead, validate that we got a successful response with expected HTTP status
		response := res.Inspect().Response
		require.True(t, response.StatusCode >= 200 && response.StatusCode < 300,
			"Expected successful HTTP status, got %d", response.StatusCode)
	}

	validateInspect := func(t *testing.T, res osapitest.Response, err error) {
		require.NotNil(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	}

	validateShrink := func(t *testing.T, res osapitest.Response, err error) {
		if isSingleNode {
			// Single-node cluster: shrink should succeed
			require.Nil(t, err, "Expected Shrink to succeed in single-node cluster")
			require.NotNil(t, res)
			require.NotNil(t, res.Inspect().Response)
			ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
		} else {
			// Multi-node cluster: shrink should fail for various reasons
			require.NotNil(t, err, "Expected Shrink to fail in multi-node cluster")
			require.NotNil(t, res)
			require.NotNil(t, res.Inspect().Response)

			// Check for expected shrink-related error messages
			expectedErrors := []string{
				"must have all shards allocated on the same node to shrink index",
				"indexMetadata\" is null",
				"Cannot invoke",
				"must block write operations to resize index",
			}

			errorFound := false
			errStr := err.Error()
			for _, expectedErr := range expectedErrors {
				if strings.Contains(errStr, expectedErr) {
					errorFound = true
					break
				}
			}

			require.True(t, errorFound, "Expected shrink-related error, got: %v", err)
		}
	}

	// Test: Create Index
	t.Run("Create", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-create")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
			validateInspect(t, res, err)
		})
	})

	// Test: Index Exists
	t.Run("Exists", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-exists")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create the index first for exists test
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			resp, err := client.Indices.Exists(nil, opensearchapi.IndicesExistsReq{Indices: []string{index}})
			require.Nil(t, err)
			require.NotNil(t, resp)
			require.Equal(t, 200, resp.StatusCode)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			resp, err := failingClient.Indices.Exists(nil, opensearchapi.IndicesExistsReq{Indices: []string{index}})
			// For inspect test, we need to wrap the response to match the Response interface
			dummyResp := osapitest.DummyInspect{Response: resp}
			validateInspect(t, dummyResp, err)
		})
	})

	// Test: Block Index
	t.Run("Block", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-block")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Block(nil, opensearchapi.IndicesBlockReq{Indices: []string{index}, Block: "write"})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Block(nil, opensearchapi.IndicesBlockReq{Indices: []string{index}, Block: "write"})
			validateInspect(t, res, err)
		})
	})

	// Test: Analyze
	t.Run("Analyze", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel - no index dependencies

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Analyze(nil, opensearchapi.IndicesAnalyzeReq{
				Body: opensearchapi.IndicesAnalyzeBody{Text: []string{"test"}, Analyzer: "standard", Explain: true},
			})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Analyze(nil, opensearchapi.IndicesAnalyzeReq{
				Body: opensearchapi.IndicesAnalyzeBody{Text: []string{"test"}, Analyzer: "standard", Explain: true},
			})
			validateInspect(t, res, err)
		})
	})

	// Test: ClearCache
	t.Run("ClearCache", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-clear-cache")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.ClearCache(nil, nil)
			validateDefault(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.ClearCache(nil, &opensearchapi.IndicesClearCacheReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.ClearCache(nil, &opensearchapi.IndicesClearCacheReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})

	// Test: Alias Operations
	t.Run("AliasOperations", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique names

		index := testutil.MustUniqueString(t, "test-alias-ops")
		alias := testutil.MustUniqueString(t, "test-alias")
		rolloverIndex := testutil.MustUniqueString(t, "test-rollover")
		t.Cleanup(func() {
			// Comprehensive cleanup for all created resources
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index, rolloverIndex},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index for alias operations
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		// Test: Alias Put (must run first to create alias)
		t.Run("AliasPut", func(t *testing.T) {
			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Alias.Put(nil, opensearchapi.AliasPutReq{Indices: []string{index}, Alias: alias})
				validateDefault(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Alias.Put(nil, opensearchapi.AliasPutReq{Indices: []string{index}, Alias: alias})
				validateInspect(t, res, err)
			})
		})

		// Test: Alias Get (run after Put completes)
		t.Run("AliasGet", func(t *testing.T) {
			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Alias.Get(nil, opensearchapi.AliasGetReq{Indices: []string{index}})
				validateDynamicIndexResponse("test-alias-ops", "aliases")(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Alias.Get(nil, opensearchapi.AliasGetReq{Indices: []string{index}})
				validateInspect(t, res, err)
			})
		})

		// Test: Alias Exists (run after Put completes)
		t.Run("AliasExists", func(t *testing.T) {
			t.Run("with_request", func(t *testing.T) {
				resp, err := client.Indices.Alias.Exists(nil, opensearchapi.AliasExistsReq{Alias: []string{alias}})
				require.Nil(t, err)
				require.NotNil(t, resp)
			})

			t.Run("inspect", func(t *testing.T) {
				resp, err := failingClient.Indices.Alias.Exists(nil, opensearchapi.AliasExistsReq{Alias: []string{alias}})
				// For inspect test, we need to wrap the response to match the Response interface
				dummyResp := osapitest.DummyInspect{Response: resp}
				validateInspect(t, dummyResp, err)
			})
		})

		// Test: Rollover (requires existing alias)
		t.Run("Rollover", func(t *testing.T) {
			// Custom validation for rollover which can fail in various ways
			validateRollover := func(t *testing.T, res osapitest.Response, err error) {
				if err != nil {
					// Check for expected rollover errors
					expectedErrors := []string{
						"rollover target",
						"does not exist",
						"source index",
						"is not managed by ILM",
					}
					errorFound := false
					errStr := err.Error()
					for _, expectedErr := range expectedErrors {
						if strings.Contains(errStr, expectedErr) {
							errorFound = true
							break
						}
					}
					if errorFound {
						require.NotNil(t, res)
						require.NotNil(t, res.Inspect().Response)
						t.Logf("Expected rollover failure: %v", err)
					} else {
						t.Errorf("Unexpected rollover error: %v", err)
					}
				} else {
					validateDefault(t, res, err)
				}
			}

			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Rollover(nil, opensearchapi.IndicesRolloverReq{Alias: alias, Index: rolloverIndex})
				validateRollover(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Rollover(nil, opensearchapi.IndicesRolloverReq{Alias: alias, Index: rolloverIndex})
				validateInspect(t, res, err)
			})
		})

		// Test: Alias Delete (run last)
		t.Run("AliasDelete", func(t *testing.T) {
			// Custom validation for alias delete which can fail when index doesn't exist
			validateAliasDelete := func(t *testing.T, res osapitest.Response, err error) {
				if err != nil {
					expectedErrors := []string{
						"index_not_found_exception",
						"no such index",
						"aliases_not_found_exception",
						"aliases missing",
					}
					errorFound := false
					errStr := err.Error()
					for _, expectedErr := range expectedErrors {
						if strings.Contains(errStr, expectedErr) {
							errorFound = true
							break
						}
					}
					if errorFound {
						require.NotNil(t, res)
						require.NotNil(t, res.Inspect().Response)
					} else {
						t.Errorf("Unexpected error for Alias Delete: %v", err)
					}
				} else {
					require.NotNil(t, res)
					require.NotNil(t, res.Inspect().Response)
				}
			}

			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Alias.Delete(nil, opensearchapi.AliasDeleteReq{Indices: []string{index}, Alias: []string{alias}})
				validateAliasDelete(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Alias.Delete(nil, opensearchapi.AliasDeleteReq{Indices: []string{index}, Alias: []string{alias}})
				validateInspect(t, res, err)
			})
		})
	})

	// Test: DataStream Operations
	t.Run("DataStreamGet", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique names

		dataStream := testutil.MustUniqueString(t, "test-datastream")
		t.Cleanup(func() {
			// Comprehensive cleanup for data stream resources
			client.DataStream.Delete(nil, opensearchapi.DataStreamDeleteReq{DataStream: dataStream})
			client.IndexTemplate.Delete(nil, opensearchapi.IndexTemplateDeleteReq{IndexTemplate: dataStream})
		})

		// Create index template and data stream
		_, err := client.IndexTemplate.Create(
			nil,
			opensearchapi.IndexTemplateCreateReq{
				IndexTemplate: dataStream,
				Body:          strings.NewReader(`{"index_patterns":["` + dataStream + `"],"template":{"settings":{"index":{"number_of_replicas":"0"}}},"priority":60,"data_stream":{}}`),
			},
		)
		require.Nil(t, err)
		_, err = client.DataStream.Create(nil, opensearchapi.DataStreamCreateReq{DataStream: dataStream})
		require.Nil(t, err)

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.DataStream.Get(nil, &opensearchapi.DataStreamGetReq{DataStreams: []string{dataStream}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.DataStream.Get(nil, &opensearchapi.DataStreamGetReq{DataStreams: []string{dataStream}})
			validateInspect(t, res, err)
		})
	})

	// Test: Resize Operations (Clone/Split/Shrink)
	t.Run("ResizeOperations", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique names

		// Test Clone
		t.Run("Clone", func(t *testing.T) {
			t.Parallel()
			srcIndex := testutil.MustUniqueString(t, "test-clone-src")
			dstIndex := testutil.MustUniqueString(t, "test-clone-dst")
			t.Cleanup(func() {
				client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
					Indices: []string{srcIndex, dstIndex},
					Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
				})
			})

			t.Run("with_request", func(t *testing.T) {
				// Create and block source index
				_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: srcIndex})
				require.Nil(t, err)
				_, err = client.Indices.Block(nil, opensearchapi.IndicesBlockReq{Indices: []string{srcIndex}, Block: "write"})
				require.Nil(t, err)

				res, err := client.Indices.Clone(nil, opensearchapi.IndicesCloneReq{Index: srcIndex, Target: dstIndex})
				validateDefault(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Clone(nil, opensearchapi.IndicesCloneReq{Index: srcIndex, Target: dstIndex})
				validateInspect(t, res, err)
			})
		})

		// Test Split
		t.Run("Split", func(t *testing.T) {
			t.Parallel()
			srcIndex := testutil.MustUniqueString(t, "test-split-src")
			dstIndex := testutil.MustUniqueString(t, "test-split-dst")
			t.Cleanup(func() {
				client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
					Indices: []string{srcIndex, dstIndex},
					Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
				})
			})

			t.Run("with_request", func(t *testing.T) {
				// Create source index with 1 shard for splitting to 2
				_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{
					Index: srcIndex,
					Body:  strings.NewReader(`{"settings":{"index":{"number_of_shards":1,"number_of_replicas":0}}}`),
				})
				require.Nil(t, err)
				_, err = client.Indices.Block(nil, opensearchapi.IndicesBlockReq{Indices: []string{srcIndex}, Block: "write"})
				require.Nil(t, err)

				res, err := client.Indices.Split(nil, opensearchapi.IndicesSplitReq{
					Index:  srcIndex,
					Target: dstIndex,
					Body:   strings.NewReader(`{"settings":{"index":{"number_of_shards":2,"number_of_replicas":0}}}`),
				})
				validateDefault(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Split(nil, opensearchapi.IndicesSplitReq{Index: srcIndex, Target: dstIndex})
				validateInspect(t, res, err)
			})
		})

		// Test Shrink
		t.Run("Shrink", func(t *testing.T) {
			t.Parallel()
			srcIndex := testutil.MustUniqueString(t, "test-shrink-src")
			dstIndex := testutil.MustUniqueString(t, "test-shrink-dst")
			t.Cleanup(func() {
				client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
					Indices: []string{srcIndex, dstIndex},
					Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
				})
			})

			t.Run("with_request", func(t *testing.T) {
				// Create source index with 2 shards for shrinking to 1
				_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{
					Index: srcIndex,
					Body:  strings.NewReader(`{"settings":{"index":{"number_of_shards":2,"number_of_replicas":0}}}`),
				})
				require.Nil(t, err)
				_, err = client.Indices.Block(nil, opensearchapi.IndicesBlockReq{Indices: []string{srcIndex}, Block: "write"})
				require.Nil(t, err)

				res, err := client.Indices.Shrink(nil, opensearchapi.IndicesShrinkReq{
					Index:  srcIndex,
					Target: dstIndex,
					Body:   strings.NewReader(`{"settings":{"index":{"number_of_shards":1,"number_of_replicas":0}}}`),
				})
				validateShrink(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Shrink(nil, opensearchapi.IndicesShrinkReq{Index: srcIndex, Target: dstIndex})
				validateInspect(t, res, err)
			})
		})
	})

	// Test: Close and Open Operations
	t.Run("CloseOpen", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-close-open")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		// Test Close (run first, not parallel with Open)
		t.Run("Close", func(t *testing.T) {
			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Close(nil, opensearchapi.IndicesCloseReq{Index: index})
				validateDefault(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Close(nil, opensearchapi.IndicesCloseReq{Index: index})
				validateInspect(t, res, err)
			})
		})

		// Test Open (run after Close completes)
		t.Run("Open", func(t *testing.T) {
			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Open(nil, opensearchapi.IndicesOpenReq{Index: index})
				validateDefault(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Open(nil, opensearchapi.IndicesOpenReq{Index: index})
				validateInspect(t, res, err)
			})
		})
	})

	// Test: Count
	t.Run("Count", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-count")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Count(nil, &opensearchapi.IndicesCountReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Count(nil, &opensearchapi.IndicesCountReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})

	// Test: Flush
	t.Run("Flush", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-flush")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Flush(nil, nil)
			validateDefault(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Flush(nil, &opensearchapi.IndicesFlushReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Flush(nil, &opensearchapi.IndicesFlushReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})

	// Test: Refresh
	t.Run("Refresh", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-refresh")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Refresh(nil, nil)
			validateDefault(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Refresh(nil, &opensearchapi.IndicesRefreshReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Refresh(nil, &opensearchapi.IndicesRefreshReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})

	// Test: Stats
	t.Run("Stats", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-stats")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Stats(nil, nil)
			validateDefault(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Stats(nil, &opensearchapi.IndicesStatsReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Stats(nil, &opensearchapi.IndicesStatsReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})

	// Test: Get Operations (Settings/Mappings)
	t.Run("Get", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-get")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Get(nil, opensearchapi.IndicesGetReq{Indices: []string{index}})
			validateDynamicIndexResponse("test-get", "aliases", "mappings", "settings")(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Get(nil, opensearchapi.IndicesGetReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})

	// Test: Settings Operations
	t.Run("Settings", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-settings")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		// Test GetSettings
		t.Run("GetSettings", func(t *testing.T) {
			t.Parallel()
			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Settings.Get(nil, &opensearchapi.SettingsGetReq{Indices: []string{index}})
				validateDynamicIndexResponse("test-settings", "settings")(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Settings.Get(nil, &opensearchapi.SettingsGetReq{Indices: []string{index}})
				validateInspect(t, res, err)
			})
		})

		// Test PutSettings
		t.Run("PutSettings", func(t *testing.T) {
			t.Parallel()
			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Settings.Put(nil, opensearchapi.SettingsPutReq{
					Indices: []string{index},
					Body:    strings.NewReader(`{"index":{"number_of_replicas":0}}`),
				})
				validateDefault(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Settings.Put(nil, opensearchapi.SettingsPutReq{
					Indices: []string{index},
					Body:    strings.NewReader(`{"index":{"number_of_replicas":0}}`),
				})
				validateInspect(t, res, err)
			})
		})
	})

	// Test: Mapping Operations
	t.Run("Mapping", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-mapping")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		// Test GetMapping
		t.Run("GetMapping", func(t *testing.T) {
			t.Parallel()
			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Mapping.Get(nil, &opensearchapi.MappingGetReq{Indices: []string{index}})
				validateDynamicIndexResponse("test-mapping", "mappings")(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Mapping.Get(nil, &opensearchapi.MappingGetReq{Indices: []string{index}})
				validateInspect(t, res, err)
			})
		})

		// Test PutMapping
		t.Run("PutMapping", func(t *testing.T) {
			t.Parallel()
			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Mapping.Put(nil, opensearchapi.MappingPutReq{
					Indices: []string{index},
					Body:    strings.NewReader(`{"properties":{"test_field":{"type":"text"}}}`),
				})
				validateDefault(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Mapping.Put(nil, opensearchapi.MappingPutReq{
					Indices: []string{index},
					Body:    strings.NewReader(`{"properties":{"test_field":{"type":"text"}}}`),
				})
				validateInspect(t, res, err)
			})
		})
	})

	// Test: Recovery
	t.Run("Recovery", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-recovery")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Recovery(nil, nil)
			// For without_request, we can't predict exact index names, so use generic validation
			validateDynamicResponse(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Recovery(nil, &opensearchapi.IndicesRecoveryReq{Indices: []string{index}})
			validateDynamicIndexResponse("test-recovery", "shards")(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Recovery(nil, &opensearchapi.IndicesRecoveryReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})

	// Test: Segments
	t.Run("Segments", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-segments")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Segments(nil, nil)
			validateDefault(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Segments(nil, &opensearchapi.IndicesSegmentsReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Segments(nil, &opensearchapi.IndicesSegmentsReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})

	// Test: ValidateQuery
	t.Run("ValidateQuery", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-validate")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.ValidateQuery(nil, opensearchapi.IndicesValidateQueryReq{
				Indices: []string{index},
				Body:    strings.NewReader(`{"query":{"match_all":{}}}`),
			})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.ValidateQuery(nil, opensearchapi.IndicesValidateQueryReq{
				Indices: []string{index},
				Body:    strings.NewReader(`{"query":{"match_all":{}}}`),
			})
			validateInspect(t, res, err)
		})
	})

	// Test: ShardStores
	t.Run("ShardStores", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-shard-stores")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.ShardStores(nil, nil)
			validateDefault(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.ShardStores(nil, &opensearchapi.IndicesShardStoresReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.ShardStores(nil, &opensearchapi.IndicesShardStoresReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})

	// Test: Resolve
	t.Run("Resolve", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel - no index dependencies

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Resolve(nil, opensearchapi.IndicesResolveReq{Indices: []string{"*"}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Resolve(nil, opensearchapi.IndicesResolveReq{Indices: []string{"*"}})
			validateInspect(t, res, err)
		})
	})

	// Test: FieldCaps
	t.Run("FieldCaps", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-field-caps")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.FieldCaps(nil, opensearchapi.IndicesFieldCapsReq{
				Params: opensearchapi.IndicesFieldCapsParams{Fields: []string{"*"}},
			})
			validateDefault(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.FieldCaps(nil, opensearchapi.IndicesFieldCapsReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesFieldCapsParams{Fields: []string{"*"}},
			})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.FieldCaps(nil, opensearchapi.IndicesFieldCapsReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesFieldCapsParams{Fields: []string{"*"}},
			})
			validateInspect(t, res, err)
		})
	})

	// Test: Forcemerge
	t.Run("Forcemerge", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-forcemerge")
		t.Cleanup(func() {
			client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
		require.Nil(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Forcemerge(nil, nil)
			validateDefault(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Forcemerge(nil, &opensearchapi.IndicesForcemergeReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Forcemerge(nil, &opensearchapi.IndicesForcemergeReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})
}

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

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	ostestutil "github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
)

// getClusterSize detects the number of nodes in the cluster for adaptive test behavior
func getClusterSize(t *testing.T, client *opensearchapi.Client) int {
	t.Helper()

	resp, err := client.Cluster.Health(t.Context(), nil)
	if err != nil {
		t.Logf("Could not detect cluster size, assuming single node: %v", err)
		return 1
	}

	nodeCount := resp.NumberOfNodes
	if ostestutil.IsDebugEnabled(t) {
		t.Logf("Detected %d node(s) in cluster", nodeCount)
	}
	return nodeCount
}

func TestIndicesClient(t *testing.T) {
	t.Parallel()
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.NoError(t, err)

	// Detect cluster size for adaptive test behavior
	clusterSize := getClusterSize(t, client)
	isSingleNode := clusterSize == 1

	// Standard validation functions
	validateDefault := func(t *testing.T, res osapitest.Response, err error) {
		t.Helper()
		require.NoError(t, err)
		require.NotNil(t, res)
		require.NotNil(t, res.Inspect().Response)
		testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
	}

	// Factory function for validating APIs with dynamic index names as top-level keys
	validateDynamicIndexResponse := func(expectedIndexPrefix string, requiredFields ...string) func(*testing.T, osapitest.Response, error) {
		return func(t *testing.T, res osapitest.Response, err error) {
			t.Helper()
			require.NoError(t, err)
			require.NotNil(t, res)
			require.NotNil(t, res.Inspect().Response)

			// Validate HTTP success
			response := res.Inspect().Response
			require.True(t, response.StatusCode >= 200 && response.StatusCode < 300,
				"Expected successful HTTP status, got %d", response.StatusCode)

			// Parse response body to validate structure
			body := res.Inspect().Response.Body
			bodyBytes, err := io.ReadAll(body)
			require.NoError(t, err, "Failed to read response body")

			var responseData map[string]any
			require.NoError(t, json.Unmarshal(bodyBytes, &responseData))

			// Find index with matching prefix
			var foundIndex string
			var indexData map[string]any
			for indexName, data := range responseData {
				if strings.HasPrefix(indexName, expectedIndexPrefix) {
					foundIndex = indexName
					var ok bool
					indexData, ok = data.(map[string]any)
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

	dataStream := testutil.MustUniqueString(t, "test-datastream-get")

	indexTemplateBody := `{` +
		`"index_patterns":["` + dataStream + `"],` +
		`"template":{"settings":{"index":{"number_of_replicas":"0"}}},` +
		`"priority":60,` +
		`"data_stream":{}` +
		`}`
	_, err = client.IndexTemplate.Create(
		t.Context(),
		opensearchapi.IndexTemplateCreateReq{
			IndexTemplate: dataStream,
			Body:          strings.NewReader(indexTemplateBody),
		},
	)
	require.NoError(t, err)

	_, err = client.DataStream.Create(t.Context(), opensearchapi.DataStreamCreateReq{DataStream: dataStream})
	require.NoError(t, err)

	t.Cleanup(func() {
		client.DataStream.Delete(t.Context(), opensearchapi.DataStreamDeleteReq{DataStream: dataStream})
		client.IndexTemplate.Delete(t.Context(), opensearchapi.IndexTemplateDeleteReq{IndexTemplate: dataStream})
	})

	// Validation function for APIs with dynamic index names as top-level keys
	// These APIs can't use strict JSON comparison due to dynamic structure
	validateDynamicResponse := func(t *testing.T, res osapitest.Response, err error) {
		t.Helper()
		require.NoError(t, err)
		require.NotNil(t, res)
		require.NotNil(t, res.Inspect().Response)
		// Skip testutil.CompareRawJSONwithParsedJSON for dynamic structure APIs
		// Instead, validate that we got a successful response with expected HTTP status
		response := res.Inspect().Response
		require.True(t, response.StatusCode >= 200 && response.StatusCode < 300,
			"Expected successful HTTP status, got %d", response.StatusCode)
	}

	validateInspect := func(t *testing.T, res osapitest.Response, err error) {
		t.Helper()
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	}

	validateShrink := func(t *testing.T, res osapitest.Response, err error) {
		t.Helper()
		if isSingleNode {
			// Single-node cluster: shrink should succeed
			require.NoError(t, err, "Expected Shrink to succeed in single-node cluster")
			require.NotNil(t, res)
			require.NotNil(t, res.Inspect().Response)
			testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
		} else {
			// Multi-node cluster: shrink should fail for various reasons
			require.Error(t, err, "Expected Shrink to fail in multi-node cluster")
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
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
			validateInspect(t, res, err)
		})
	})

	// Test: Index Exists
	t.Run("Exists", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-exists")
		t.Cleanup(func() {
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create the index first for exists test
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			resp, err := client.Indices.Exists(t.Context(), opensearchapi.IndicesExistsReq{Indices: []string{index}})
			require.NoError(t, err)
			require.NotNil(t, resp)
			require.Equal(t, 200, resp.StatusCode)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			resp, err := failingClient.Indices.Exists(t.Context(), opensearchapi.IndicesExistsReq{Indices: []string{index}})
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
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Block(t.Context(), opensearchapi.IndicesBlockReq{Indices: []string{index}, Block: "write"})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Block(t.Context(), opensearchapi.IndicesBlockReq{Indices: []string{index}, Block: "write"})
			validateInspect(t, res, err)
		})
	})

	// Test: Analyze
	t.Run("Analyze", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel - no index dependencies

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Analyze(t.Context(), opensearchapi.IndicesAnalyzeReq{
				Body: opensearchapi.IndicesAnalyzeBody{Text: []string{"test"}, Analyzer: "standard", Explain: true},
			})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Analyze(t.Context(), opensearchapi.IndicesAnalyzeReq{
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
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.ClearCache(t.Context(), nil)
			validateDefault(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.ClearCache(t.Context(), &opensearchapi.IndicesClearCacheReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.ClearCache(t.Context(), &opensearchapi.IndicesClearCacheReq{Indices: []string{index}})
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
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index, rolloverIndex},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index for alias operations
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		// Test: Alias Put (must run first to create alias)
		t.Run("AliasPut", func(t *testing.T) {
			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Alias.Put(t.Context(), opensearchapi.AliasPutReq{Indices: []string{index}, Alias: alias})
				validateDefault(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Alias.Put(t.Context(), opensearchapi.AliasPutReq{Indices: []string{index}, Alias: alias})
				validateInspect(t, res, err)
			})
		})

		// Test: Alias Get (run after Put completes)
		t.Run("AliasGet", func(t *testing.T) {
			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Alias.Get(t.Context(), opensearchapi.AliasGetReq{Indices: []string{index}})
				validateDynamicIndexResponse("test-alias-ops", "aliases")(t, res, err)

				// Test GetIndices() method for coverage
				indices := res.GetIndices()
				require.NotNil(t, indices, "GetIndices should return a non-nil map")
				require.Contains(t, indices, index, "GetIndices should contain the created index")
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Alias.Get(t.Context(), opensearchapi.AliasGetReq{Indices: []string{index}})
				validateInspect(t, res, err)
			})
		})

		// Test: Alias Exists (run after Put completes)
		t.Run("AliasExists", func(t *testing.T) {
			t.Run("with_request", func(t *testing.T) {
				resp, err := client.Indices.Alias.Exists(t.Context(), opensearchapi.AliasExistsReq{Alias: []string{alias}})
				require.NoError(t, err)
				require.NotNil(t, resp)
			})

			t.Run("inspect", func(t *testing.T) {
				resp, err := failingClient.Indices.Alias.Exists(t.Context(), opensearchapi.AliasExistsReq{Alias: []string{alias}})
				// For inspect test, we need to wrap the response to match the Response interface
				dummyResp := osapitest.DummyInspect{Response: resp}
				validateInspect(t, dummyResp, err)
			})
		})

		// Test: Rollover (requires existing alias)
		t.Run("Rollover", func(t *testing.T) {
			// Custom validation for rollover which can fail in various ways
			validateRollover := func(t *testing.T, res osapitest.Response, err error) {
				t.Helper()
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
				res, err := client.Indices.Rollover(t.Context(), opensearchapi.IndicesRolloverReq{Alias: alias, Index: rolloverIndex})
				validateRollover(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Rollover(t.Context(), opensearchapi.IndicesRolloverReq{Alias: alias, Index: rolloverIndex})
				validateInspect(t, res, err)
			})
		})

		// Test: Alias Delete (run last)
		t.Run("AliasDelete", func(t *testing.T) {
			// Custom validation for alias delete which can fail when index doesn't exist
			validateAliasDelete := func(t *testing.T, res osapitest.Response, err error) {
				t.Helper()
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
				res, err := client.Indices.Alias.Delete(t.Context(), opensearchapi.AliasDeleteReq{Indices: []string{index}, Alias: []string{alias}})
				validateAliasDelete(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Alias.Delete(
					t.Context(),
					opensearchapi.AliasDeleteReq{Indices: []string{index}, Alias: []string{alias}},
				)
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
			client.DataStream.Delete(t.Context(), opensearchapi.DataStreamDeleteReq{DataStream: dataStream})
			client.IndexTemplate.Delete(t.Context(), opensearchapi.IndexTemplateDeleteReq{IndexTemplate: dataStream})
		})

		// Create index template and data stream
		_, err := client.IndexTemplate.Create(
			t.Context(),
			opensearchapi.IndexTemplateCreateReq{
				IndexTemplate: dataStream,
				Body: strings.NewReader(`{"index_patterns":["` + dataStream + `"],"template":{"settings":` +
					`{"index":{"number_of_replicas":"0"}}},"priority":60,"data_stream":{}}`),
			},
		)
		require.NoError(t, err)
		_, err = client.DataStream.Create(t.Context(), opensearchapi.DataStreamCreateReq{DataStream: dataStream})
		require.NoError(t, err)

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.DataStream.Get(t.Context(), &opensearchapi.DataStreamGetReq{DataStreams: []string{dataStream}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.DataStream.Get(t.Context(), &opensearchapi.DataStreamGetReq{DataStreams: []string{dataStream}})
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
				client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
					Indices: []string{srcIndex, dstIndex},
					Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
				})
			})

			t.Run("with_request", func(t *testing.T) {
				// Create and block source index
				_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: srcIndex})
				require.NoError(t, err)
				_, err = client.Indices.Block(t.Context(), opensearchapi.IndicesBlockReq{Indices: []string{srcIndex}, Block: "write"})
				require.NoError(t, err)

				res, err := client.Indices.Clone(t.Context(), opensearchapi.IndicesCloneReq{Index: srcIndex, Target: dstIndex})
				validateDefault(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Clone(t.Context(), opensearchapi.IndicesCloneReq{Index: srcIndex, Target: dstIndex})
				validateInspect(t, res, err)
			})
		})

		// Test Split
		t.Run("Split", func(t *testing.T) {
			t.Parallel()
			srcIndex := testutil.MustUniqueString(t, "test-split-src")
			dstIndex := testutil.MustUniqueString(t, "test-split-dst")
			t.Cleanup(func() {
				client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
					Indices: []string{srcIndex, dstIndex},
					Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
				})
			})

			t.Run("with_request", func(t *testing.T) {
				// Create source index with 1 shard for splitting to 2
				_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{
					Index: srcIndex,
					Body:  strings.NewReader(`{"settings":{"index":{"number_of_shards":1,"number_of_replicas":0}}}`),
				})
				require.NoError(t, err)
				_, err = client.Indices.Block(t.Context(), opensearchapi.IndicesBlockReq{Indices: []string{srcIndex}, Block: "write"})
				require.NoError(t, err)

				res, err := client.Indices.Split(t.Context(), opensearchapi.IndicesSplitReq{
					Index:  srcIndex,
					Target: dstIndex,
					Body:   strings.NewReader(`{"settings":{"index":{"number_of_shards":2,"number_of_replicas":0}}}`),
				})
				validateDefault(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Split(t.Context(), opensearchapi.IndicesSplitReq{Index: srcIndex, Target: dstIndex})
				validateInspect(t, res, err)
			})
		})

		// Test Shrink
		t.Run("Shrink", func(t *testing.T) {
			t.Parallel()
			srcIndex := testutil.MustUniqueString(t, "test-shrink-src")
			dstIndex := testutil.MustUniqueString(t, "test-shrink-dst")
			t.Cleanup(func() {
				client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
					Indices: []string{srcIndex, dstIndex},
					Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
				})
			})

			t.Run("with_request", func(t *testing.T) {
				// Create source index with 2 shards for shrinking to 1
				_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{
					Index: srcIndex,
					Body:  strings.NewReader(`{"settings":{"index":{"number_of_shards":2,"number_of_replicas":0}}}`),
				})
				require.NoError(t, err)

				// Ensure index exists before proceeding (eventual consistency)
				resp, err := client.Indices.Exists(t.Context(), opensearchapi.IndicesExistsReq{Indices: []string{srcIndex}})
				require.NoError(t, err)
				require.Equal(t, 200, resp.StatusCode, "Index must exist before blocking")

				_, err = client.Indices.Block(t.Context(), opensearchapi.IndicesBlockReq{Indices: []string{srcIndex}, Block: "write"})
				require.NoError(t, err)

				res, err := client.Indices.Shrink(t.Context(), opensearchapi.IndicesShrinkReq{
					Index:  srcIndex,
					Target: dstIndex,
					Body:   strings.NewReader(`{"settings":{"index":{"number_of_shards":1,"number_of_replicas":0}}}`),
				})
				validateShrink(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Shrink(t.Context(), opensearchapi.IndicesShrinkReq{Index: srcIndex, Target: dstIndex})
				validateInspect(t, res, err)
			})
		})
	})

	// Test: Close and Open Operations
	t.Run("CloseOpen", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-close-open")
		t.Cleanup(func() {
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		// Test Close (run first, not parallel with Open)
		t.Run("Close", func(t *testing.T) {
			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Close(t.Context(), opensearchapi.IndicesCloseReq{Index: index})
				validateDefault(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Close(t.Context(), opensearchapi.IndicesCloseReq{Index: index})
				validateInspect(t, res, err)
			})
		})

		// Test Open (run after Close completes)
		t.Run("Open", func(t *testing.T) {
			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Open(t.Context(), opensearchapi.IndicesOpenReq{Index: index})
				validateDefault(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Open(t.Context(), opensearchapi.IndicesOpenReq{Index: index})
				validateInspect(t, res, err)
			})
		})
	})

	// Test: Count
	t.Run("Count", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-count")
		t.Cleanup(func() {
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Count(t.Context(), &opensearchapi.IndicesCountReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Count(t.Context(), &opensearchapi.IndicesCountReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})

	// Test: Flush
	t.Run("Flush", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-flush")
		t.Cleanup(func() {
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			// Flush only this test's index to avoid conflicts with closed indices from other parallel tests
			res, err := client.Indices.Flush(t.Context(), &opensearchapi.IndicesFlushReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Flush(t.Context(), &opensearchapi.IndicesFlushReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Flush(t.Context(), &opensearchapi.IndicesFlushReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})

	// Test: Refresh
	t.Run("Refresh", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-refresh")
		t.Cleanup(func() {
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			// Refresh only this test's index to avoid conflicts with closed indices from other parallel tests
			res, err := client.Indices.Refresh(t.Context(), &opensearchapi.IndicesRefreshReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Refresh(t.Context(), &opensearchapi.IndicesRefreshReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Refresh(t.Context(), &opensearchapi.IndicesRefreshReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})

	// Test: Stats
	t.Run("Stats", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-stats")
		t.Cleanup(func() {
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Stats(t.Context(), nil)
			validateDefault(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Stats(t.Context(), &opensearchapi.IndicesStatsReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Stats(t.Context(), &opensearchapi.IndicesStatsReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})

	// Test: Get Operations (Settings/Mappings)
	t.Run("Get", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-get")
		t.Cleanup(func() {
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Get(t.Context(), opensearchapi.IndicesGetReq{Indices: []string{index}})
			validateDynamicIndexResponse("test-get", "aliases", "mappings", "settings")(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Get(t.Context(), opensearchapi.IndicesGetReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})

	// Test: Settings Operations
	t.Run("Settings", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-settings")
		t.Cleanup(func() {
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		// Test GetSettings
		t.Run("GetSettings", func(t *testing.T) {
			t.Parallel()
			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Settings.Get(t.Context(), &opensearchapi.SettingsGetReq{Indices: []string{index}})
				validateDynamicIndexResponse("test-settings", "settings")(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Settings.Get(t.Context(), &opensearchapi.SettingsGetReq{Indices: []string{index}})
				validateInspect(t, res, err)
			})
		})

		// Test PutSettings
		t.Run("PutSettings", func(t *testing.T) {
			t.Parallel()
			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Settings.Put(t.Context(), opensearchapi.SettingsPutReq{
					Indices: []string{index},
					Body:    strings.NewReader(`{"index":{"number_of_replicas":0}}`),
				})
				validateDefault(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Settings.Put(t.Context(), opensearchapi.SettingsPutReq{
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
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		// Test GetMapping
		t.Run("GetMapping", func(t *testing.T) {
			t.Parallel()
			t.Run("with_nil_request", func(t *testing.T) {
				res, err := client.Indices.Mapping.Get(t.Context(), nil)
				require.NoError(t, err)
				require.NotNil(t, res)
				require.NotNil(t, res.Inspect().Response)
			})

			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Mapping.Get(t.Context(), &opensearchapi.MappingGetReq{Indices: []string{index}})
				validateDynamicIndexResponse("test-mapping", "mappings")(t, res, err)

				// Test GetIndices() method for coverage
				indices := res.GetIndices()
				require.NotNil(t, indices, "GetIndices should return a non-nil map")
				require.Contains(t, indices, index, "GetIndices should contain the created index")
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Mapping.Get(t.Context(), &opensearchapi.MappingGetReq{Indices: []string{index}})
				validateInspect(t, res, err)
			})
		})

		// Test PutMapping (must run before FieldMapping)
		t.Run("PutMapping", func(t *testing.T) {
			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Mapping.Put(t.Context(), opensearchapi.MappingPutReq{
					Indices: []string{index},
					Body:    strings.NewReader(`{"properties":{"test_field":{"type":"text"}}}`),
				})
				validateDefault(t, res, err)
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Mapping.Put(t.Context(), opensearchapi.MappingPutReq{
					Indices: []string{index},
					Body:    strings.NewReader(`{"properties":{"test_field":{"type":"text"}}}`),
				})
				validateInspect(t, res, err)
			})
		})

		// Test Field Mapping (requires PutMapping to have run)
		t.Run("FieldMapping", func(t *testing.T) {
			t.Run("with_nil_request", func(t *testing.T) {
				// Nil request is valid code path, but OpenSearch will error because fields are required
				res, err := client.Indices.Mapping.Field(t.Context(), nil)
				require.Error(t, err, "OpenSearch should reject empty field list")
				require.NotNil(t, res)
				require.NotNil(t, res.Inspect().Response)
			})

			t.Run("with_request", func(t *testing.T) {
				res, err := client.Indices.Mapping.Field(t.Context(), &opensearchapi.MappingFieldReq{
					Indices: []string{index},
					Fields:  []string{"test_field"},
				})
				validateDynamicIndexResponse("test-mapping", "mappings")(t, res, err)

				// Test GetIndices() method for coverage
				indices := res.GetIndices()
				require.NotNil(t, indices, "GetIndices should return a non-nil map")
				require.Contains(t, indices, index, "GetIndices should contain the created index")
			})

			t.Run("inspect", func(t *testing.T) {
				res, err := failingClient.Indices.Mapping.Field(t.Context(), &opensearchapi.MappingFieldReq{
					Indices: []string{index},
					Fields:  []string{"test_field"},
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
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Recovery(t.Context(), nil)
			// For without_request, we can't predict exact index names, so use generic validation
			validateDynamicResponse(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Recovery(t.Context(), &opensearchapi.IndicesRecoveryReq{Indices: []string{index}})
			validateDynamicIndexResponse("test-recovery", "shards")(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Recovery(t.Context(), &opensearchapi.IndicesRecoveryReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})

	// Test: Segments
	t.Run("Segments", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-segments")
		t.Cleanup(func() {
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Segments(t.Context(), nil)
			validateDefault(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Segments(t.Context(), &opensearchapi.IndicesSegmentsReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Segments(t.Context(), &opensearchapi.IndicesSegmentsReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})

	// Test: ValidateQuery
	t.Run("ValidateQuery", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-validate")
		t.Cleanup(func() {
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.ValidateQuery(t.Context(), opensearchapi.IndicesValidateQueryReq{
				Indices: []string{index},
				Body:    strings.NewReader(`{"query":{"match_all":{}}}`),
			})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.ValidateQuery(t.Context(), opensearchapi.IndicesValidateQueryReq{
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
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.ShardStores(t.Context(), nil)
			validateDefault(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.ShardStores(t.Context(), &opensearchapi.IndicesShardStoresReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.ShardStores(t.Context(), &opensearchapi.IndicesShardStoresReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})

	// Test: Resolve
	t.Run("Resolve", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel - no index dependencies

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Resolve(t.Context(), opensearchapi.IndicesResolveReq{Indices: []string{"*"}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Resolve(t.Context(), opensearchapi.IndicesResolveReq{Indices: []string{"*"}})
			validateInspect(t, res, err)
		})
	})

	// Test: FieldCaps
	t.Run("FieldCaps", func(t *testing.T) {
		t.Parallel() // Safe to run in parallel with unique index names

		index := testutil.MustUniqueString(t, "test-field-caps")
		t.Cleanup(func() {
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.FieldCaps(t.Context(), opensearchapi.IndicesFieldCapsReq{
				Params: opensearchapi.IndicesFieldCapsParams{Fields: []string{"*"}},
			})
			validateDefault(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.FieldCaps(t.Context(), opensearchapi.IndicesFieldCapsReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesFieldCapsParams{Fields: []string{"*"}},
			})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.FieldCaps(t.Context(), opensearchapi.IndicesFieldCapsReq{
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
			client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{
				Indices: []string{index},
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			})
		})

		// Create index first
		_, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
		require.NoError(t, err)

		t.Run("without_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Forcemerge(t.Context(), nil)
			validateDefault(t, res, err)
		})

		t.Run("with_request", func(t *testing.T) {
			t.Parallel()
			res, err := client.Indices.Forcemerge(t.Context(), &opensearchapi.IndicesForcemergeReq{Indices: []string{index}})
			validateDefault(t, res, err)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Indices.Forcemerge(t.Context(), &opensearchapi.IndicesForcemergeReq{Indices: []string{index}})
			validateInspect(t, res, err)
		})
	})
}

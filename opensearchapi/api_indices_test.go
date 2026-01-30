// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
)

func TestIndicesClient(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.NoError(t, err)

	index := testutil.MustUniqueString(t, "test-indices-create")
	indexClone := testutil.MustUniqueString(t, "test-indices-clone")
	indexSplit := testutil.MustUniqueString(t, "test-indices-split")
	indexShrink := testutil.MustUniqueString(t, "test-indices-shrink")
	indexRollover := testutil.MustUniqueString(t, "test-indices-rollover")
	testIndices := []string{index, indexClone, indexSplit, indexShrink, indexRollover}

	alias := testutil.MustUniqueString(t, "test-indices-alias")
	dataStream := testutil.MustUniqueString(t, "test-datastream-get")

	_, err = client.IndexTemplate.Create(
		t.Context(),
		opensearchapi.IndexTemplateCreateReq{
			IndexTemplate: dataStream,
			Body: strings.NewReader(fmt.Sprintf(
				`{"index_patterns":["%s"],"template":{"settings":{"index":{"number_of_replicas":"0"}}},"priority":60,"data_stream":{}}`,
				dataStream)),
		},
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		client.Indices.Delete(
			t.Context(),
			opensearchapi.IndicesDeleteReq{
				Indices: testIndices,
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			},
		)
		client.DataStream.Delete(t.Context(), opensearchapi.DataStreamDeleteReq{DataStream: dataStream})
		client.IndexTemplate.Delete(t.Context(), opensearchapi.IndexTemplateDeleteReq{IndexTemplate: dataStream})
	})
	_, err = client.DataStream.Create(t.Context(), opensearchapi.DataStreamCreateReq{DataStream: dataStream})
	require.NoError(t, err)

	type indicesTests struct {
		Name    string
		Results func() (osapitest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []indicesTests
	}{
		{
			Name: "Create",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
					},
				},
			},
		},
		{
			Name: "Exists",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						var (
							resp osapitest.DummyInspect
							err  error
						)
						resp.Response, err = client.Indices.Exists(t.Context(), opensearchapi.IndicesExistsReq{Indices: []string{index}})
						return resp, err
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						var (
							resp osapitest.DummyInspect
							err  error
						)
						resp.Response, err = failingClient.Indices.Exists(t.Context(), opensearchapi.IndicesExistsReq{Indices: []string{index}})
						return resp, err
					},
				},
			},
		},
		{
			Name: "Block",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Block(t.Context(), opensearchapi.IndicesBlockReq{Indices: []string{index}, Block: "write"})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Block(t.Context(), opensearchapi.IndicesBlockReq{Indices: []string{index}, Block: "write"})
					},
				},
			},
		},
		{
			Name: "Analyze",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Analyze(t.Context(),
							opensearchapi.IndicesAnalyzeReq{Body: opensearchapi.IndicesAnalyzeBody{Text: []string{"test"}, Analyzer: "standard"}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Analyze(t.Context(),
							opensearchapi.IndicesAnalyzeReq{Body: opensearchapi.IndicesAnalyzeBody{Text: []string{"test"}, Analyzer: "standard"}})
					},
				},
			},
		},
		{
			Name: "ClearCache",
			Tests: []indicesTests{
				{
					Name: "without request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.ClearCache(t.Context(), nil)
					},
				},
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.ClearCache(t.Context(), &opensearchapi.IndicesClearCacheReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.ClearCache(t.Context(), nil)
					},
				},
			},
		},
		{
			Name: "Alias Put",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Alias.Put(t.Context(), opensearchapi.AliasPutReq{Indices: []string{index}, Alias: alias})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Alias.Put(t.Context(), opensearchapi.AliasPutReq{Indices: []string{index}, Alias: alias})
					},
				},
			},
		},
		{
			Name: "Alias Get",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Alias.Get(t.Context(), opensearchapi.AliasGetReq{Indices: []string{index}, Alias: []string{alias}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Alias.Get(t.Context(), opensearchapi.AliasGetReq{Indices: []string{index}, Alias: []string{alias}})
					},
				},
			},
		},
		{
			Name: "Alias Exists",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						var (
							resp osapitest.DummyInspect
							err  error
						)
						resp.Response, err = client.Indices.Alias.Exists(
							t.Context(), opensearchapi.AliasExistsReq{Indices: []string{index}, Alias: []string{alias}})
						return resp, err
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						var (
							resp osapitest.DummyInspect
							err  error
						)
						resp.Response, err = failingClient.Indices.Alias.Exists(
							t.Context(), opensearchapi.AliasExistsReq{Indices: []string{index}, Alias: []string{alias}})
						return resp, err
					},
				},
			},
		},
		{
			Name: "DataSteam Indice Get",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Get(t.Context(), opensearchapi.IndicesGetReq{Indices: []string{fmt.Sprintf(".ds-%s-000001", dataStream)}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Get(t.Context(),
							opensearchapi.IndicesGetReq{Indices: []string{fmt.Sprintf(".ds-%s-000001", dataStream)}})
					},
				},
			},
		},
		{
			Name: "Rollover",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Rollover(t.Context(), opensearchapi.IndicesRolloverReq{Alias: alias, Index: indexRollover})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Rollover(t.Context(), opensearchapi.IndicesRolloverReq{Alias: alias, Index: indexRollover})
					},
				},
			},
		},
		{
			Name: "Alias Delete",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Alias.Delete(t.Context(),
							opensearchapi.AliasDeleteReq{Indices: []string{indexRollover}, Alias: []string{alias}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Alias.Delete(t.Context(),
							opensearchapi.AliasDeleteReq{Indices: []string{indexRollover}, Alias: []string{alias}})
					},
				},
			},
		},
		{
			Name: "Mapping Put",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Mapping.Put(t.Context(),
							opensearchapi.MappingPutReq{Indices: []string{index}, Body: strings.NewReader(`{"properties":{"test":{"type":"text"}}}`)})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Mapping.Put(t.Context(),
							opensearchapi.MappingPutReq{Indices: []string{index}, Body: strings.NewReader(`{"properties":{"test":{"type":"text"}}}`)})
					},
				},
			},
		},
		{
			Name: "Mapping Get",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Mapping.Get(t.Context(), &opensearchapi.MappingGetReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Mapping.Get(t.Context(), &opensearchapi.MappingGetReq{Indices: []string{index}})
					},
				},
			},
		},
		{
			Name: "Mapping Field",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Mapping.Field(t.Context(), &opensearchapi.MappingFieldReq{Indices: []string{index}, Fields: []string{"*"}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Mapping.Field(t.Context(),
							&opensearchapi.MappingFieldReq{Indices: []string{index}, Fields: []string{"*"}})
					},
				},
			},
		},
		{
			Name: "Settings Put",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Settings.Put(t.Context(),
							opensearchapi.SettingsPutReq{Indices: []string{index}, Body: strings.NewReader(`{"number_of_replicas":0}`)})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Settings.Put(t.Context(),
							opensearchapi.SettingsPutReq{Indices: []string{index}, Body: strings.NewReader(`{"number_of_replicas":1}`)})
					},
				},
			},
		},
		{
			Name: "Settings Get",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Settings.Get(t.Context(), &opensearchapi.SettingsGetReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Settings.Get(t.Context(), &opensearchapi.SettingsGetReq{Indices: []string{index}})
					},
				},
			},
		},
		{
			Name: "Flush",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Flush(t.Context(), &opensearchapi.IndicesFlushReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Flush(t.Context(), &opensearchapi.IndicesFlushReq{Indices: []string{index}})
					},
				},
			},
		},
		{
			Name: "Forcemerge",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Forcemerge(t.Context(), &opensearchapi.IndicesForcemergeReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Forcemerge(t.Context(), &opensearchapi.IndicesForcemergeReq{Indices: []string{index}})
					},
				},
			},
		},
		{
			Name: "Clone",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Clone(t.Context(), opensearchapi.IndicesCloneReq{Index: index, Target: indexClone})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Clone(t.Context(), opensearchapi.IndicesCloneReq{Index: index, Target: indexClone})
					},
				},
			},
		},
		{
			Name: "Split",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Split(
							t.Context(),
							opensearchapi.IndicesSplitReq{
								Index:  index,
								Target: indexSplit,
								Body:   strings.NewReader(`{"settings":{"index":{"number_of_shards":2,"number_of_replicas":0}}}`),
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Split(t.Context(), opensearchapi.IndicesSplitReq{Index: index, Target: indexSplit})
					},
				},
			},
		},
		{
			Name: "Shrink",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Shrink(
							t.Context(),
							opensearchapi.IndicesShrinkReq{
								Index:  indexSplit,
								Target: indexShrink,
								Body:   strings.NewReader(`{"settings":{"index":{"number_of_shards":1,"number_of_replicas":0}}}`),
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Shrink(t.Context(), opensearchapi.IndicesShrinkReq{Index: index, Target: indexClone})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Get(t.Context(), opensearchapi.IndicesGetReq{Indices: []string{index, indexClone}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Get(t.Context(), opensearchapi.IndicesGetReq{Indices: []string{index, indexClone}})
					},
				},
			},
		},
		{
			Name: "Recovery",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Recovery(t.Context(), &opensearchapi.IndicesRecoveryReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Recovery(t.Context(), &opensearchapi.IndicesRecoveryReq{Indices: []string{index}})
					},
				},
			},
		},
		{
			Name: "Refresh",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Refresh(t.Context(), &opensearchapi.IndicesRefreshReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Refresh(t.Context(), &opensearchapi.IndicesRefreshReq{Indices: []string{index}})
					},
				},
			},
		},
		{
			Name: "Segments",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Segments(t.Context(), &opensearchapi.IndicesSegmentsReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Segments(t.Context(), &opensearchapi.IndicesSegmentsReq{Indices: []string{index}})
					},
				},
			},
		},
		{
			Name: "ShardStores",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.ShardStores(t.Context(), &opensearchapi.IndicesShardStoresReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.ShardStores(t.Context(), &opensearchapi.IndicesShardStoresReq{Indices: []string{index}})
					},
				},
			},
		},
		{
			Name: "Stats",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Stats(t.Context(), &opensearchapi.IndicesStatsReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Stats(t.Context(), &opensearchapi.IndicesStatsReq{Indices: []string{index}})
					},
				},
			},
		},
		{
			Name: "ValidateQuery",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.ValidateQuery(t.Context(), opensearchapi.IndicesValidateQueryReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.ValidateQuery(t.Context(), opensearchapi.IndicesValidateQueryReq{Indices: []string{index}})
					},
				},
			},
		},
		{
			Name: "Count",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Count(
							t.Context(),
							&opensearchapi.IndicesCountReq{
								Indices: []string{index},
								Body:    strings.NewReader(`{"query":{"match":{"test":"TEXT"}}}`),
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Count(t.Context(), nil)
					},
				},
			},
		},
		{
			Name: "FieldCaps",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.FieldCaps(
							t.Context(),
							opensearchapi.IndicesFieldCapsReq{
								Indices: []string{index},
								Params:  opensearchapi.IndicesFieldCapsParams{Fields: []string{"*"}},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.FieldCaps(
							t.Context(),
							opensearchapi.IndicesFieldCapsReq{
								Params: opensearchapi.IndicesFieldCapsParams{Fields: []string{"*"}},
							},
						)
					},
				},
			},
		},
		{
			Name: "Resolve",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Resolve(t.Context(), opensearchapi.IndicesResolveReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Resolve(t.Context(), opensearchapi.IndicesResolveReq{})
					},
				},
			},
		},
		{
			Name: "Close",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Close(t.Context(), opensearchapi.IndicesCloseReq{Index: index})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Close(t.Context(), opensearchapi.IndicesCloseReq{Index: index})
					},
				},
			},
		},
		{
			Name: "Open",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Open(t.Context(), opensearchapi.IndicesOpenReq{Index: index})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Open(t.Context(), opensearchapi.IndicesOpenReq{Index: index})
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []indicesTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{Indices: testIndices})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{Indices: testIndices})
					},
				},
			},
		},
	}
	for _, value := range testCases {
		t.Run(value.Name, func(t *testing.T) {
			for _, testCase := range value.Tests {
				t.Run(testCase.Name, func(t *testing.T) {
					res, err := testCase.Results()
					if testCase.Name == "inspect" {
						require.Error(t, err)
						assert.NotNil(t, res)
						osapitest.VerifyInspect(t, res.Inspect())
					} else {
						require.NoError(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
					}
				})
			}
		})
	}

	t.Run("ValidateResponse", func(t *testing.T) {
		_, err = client.Indices.Delete(
			t.Context(),
			opensearchapi.IndicesDeleteReq{
				Indices: testIndices,
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			},
		)
		require.NoError(t, err)

		t.Run("Create", func(t *testing.T) {
			resp, err := client.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: index})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Block", func(t *testing.T) {
			resp, err := client.Indices.Block(t.Context(), opensearchapi.IndicesBlockReq{Indices: []string{index}, Block: "write"})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Analyze", func(t *testing.T) {
			resp, err := client.Indices.Analyze(t.Context(),
				opensearchapi.IndicesAnalyzeReq{Body: opensearchapi.IndicesAnalyzeBody{Text: []string{"test"}, Analyzer: "standard", Explain: true}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("ClearCache", func(t *testing.T) {
			resp, err := client.Indices.ClearCache(t.Context(), &opensearchapi.IndicesClearCacheReq{Indices: []string{index}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Alias Put", func(t *testing.T) {
			resp, err := client.Indices.Alias.Put(t.Context(), opensearchapi.AliasPutReq{Indices: []string{index}, Alias: alias})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Alias Get", func(t *testing.T) {
			resp, err := client.Indices.Alias.Get(t.Context(), opensearchapi.AliasGetReq{Indices: []string{index}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Indices, resp.Inspect().Response)
		})
		t.Run("Rollover", func(t *testing.T) {
			resp, err := client.Indices.Rollover(t.Context(), opensearchapi.IndicesRolloverReq{Alias: alias, Index: indexRollover})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Alias Delete", func(t *testing.T) {
			resp, err := client.Indices.Alias.Delete(t.Context(),
				opensearchapi.AliasDeleteReq{Indices: []string{indexRollover}, Alias: []string{alias}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Mapping Put", func(t *testing.T) {
			resp, err := client.Indices.Mapping.Put(t.Context(),
				opensearchapi.MappingPutReq{Indices: []string{index}, Body: strings.NewReader(`{"properties":{"test":{"type":"text"}}}`)})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Mapping Get", func(t *testing.T) {
			resp, err := client.Indices.Mapping.Get(t.Context(), &opensearchapi.MappingGetReq{Indices: []string{index}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Indices, resp.Inspect().Response)
		})
		t.Run("Mapping Field", func(t *testing.T) {
			resp, err := client.Indices.Mapping.Field(t.Context(), &opensearchapi.MappingFieldReq{Fields: []string{"*"}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Indices, resp.Inspect().Response)
		})
		t.Run("Settings Put", func(t *testing.T) {
			resp, err := client.Indices.Settings.Put(t.Context(),
				opensearchapi.SettingsPutReq{Indices: []string{index}, Body: strings.NewReader(`{"number_of_replicas":1}`)})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Settings Get", func(t *testing.T) {
			resp, err := client.Indices.Settings.Get(t.Context(), &opensearchapi.SettingsGetReq{Indices: []string{index}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Indices, resp.Inspect().Response)
		})
		t.Run("Flush", func(t *testing.T) {
			resp, err := client.Indices.Flush(t.Context(), &opensearchapi.IndicesFlushReq{Indices: []string{index}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Forcemerge", func(t *testing.T) {
			resp, err := client.Indices.Forcemerge(t.Context(), &opensearchapi.IndicesForcemergeReq{Indices: []string{index}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Clone", func(t *testing.T) {
			resp, err := client.Indices.Clone(t.Context(), opensearchapi.IndicesCloneReq{Index: index, Target: indexClone})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Split", func(t *testing.T) {
			resp, err := client.Indices.Split(
				t.Context(),
				opensearchapi.IndicesSplitReq{
					Index:  index,
					Target: indexSplit,
					Body:   strings.NewReader(`{"settings":{"index":{"number_of_shards":2,"number_of_replicas":0}}}`),
				},
			)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Shrink", func(t *testing.T) {
			resp, err := client.Indices.Shrink(
				t.Context(),
				opensearchapi.IndicesShrinkReq{
					Index:  indexSplit,
					Target: indexShrink,
					Body:   strings.NewReader(`{"settings":{"index":{"number_of_shards":1,"number_of_replicas":0}}}`),
				},
			)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Get", func(t *testing.T) {
			resp, err := client.Indices.Get(t.Context(), opensearchapi.IndicesGetReq{Indices: []string{index, indexClone}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Indices, resp.Inspect().Response)
		})
		t.Run("Recovery", func(t *testing.T) {
			resp, err := client.Indices.Recovery(t.Context(), &opensearchapi.IndicesRecoveryReq{Indices: []string{index}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Indices, resp.Inspect().Response)
		})
		t.Run("Refresh", func(t *testing.T) {
			resp, err := client.Indices.Refresh(t.Context(), &opensearchapi.IndicesRefreshReq{Indices: []string{index}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Segments", func(t *testing.T) {
			resp, err := client.Indices.Segments(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("ShardStores", func(t *testing.T) {
			resp, err := client.Indices.ShardStores(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Stats", func(t *testing.T) {
			resp, err := client.Indices.Stats(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("ValidateQuery", func(t *testing.T) {
			resp, err := client.Indices.ValidateQuery(
				t.Context(),
				opensearchapi.IndicesValidateQueryReq{
					Indices: []string{index},
					Params: opensearchapi.IndicesValidateQueryParams{
						Rewrite: opensearchapi.ToPointer(true),
					},
				},
			)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Count", func(t *testing.T) {
			resp, err := client.Indices.Count(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("FieldCaps", func(t *testing.T) {
			resp, err := client.Indices.FieldCaps(
				t.Context(),
				opensearchapi.IndicesFieldCapsReq{
					Params: opensearchapi.IndicesFieldCapsParams{Fields: []string{"*"}},
				},
			)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Resolve", func(t *testing.T) {
			resp, err := client.Indices.Resolve(t.Context(), opensearchapi.IndicesResolveReq{Indices: []string{"*"}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Close", func(t *testing.T) {
			resp, err := client.Indices.Close(t.Context(), opensearchapi.IndicesCloseReq{Index: index})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Open", func(t *testing.T) {
			resp, err := client.Indices.Open(t.Context(), opensearchapi.IndicesOpenReq{Index: index})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Delete", func(t *testing.T) {
			resp, err := client.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{Indices: testIndices})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	})
}

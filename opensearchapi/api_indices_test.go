// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
)

func TestIndicesClient(t *testing.T) {
	client, err := ostest.NewClient()
	require.Nil(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	index := "test-indices-create"
	indexClone := "test-indices-clone"
	indexSplit := "test-indices-split"
	indexShrink := "test-indices-shrink"
	indexRollover := "test-indices-rollover"
	alias := "test-indices-alias"
	testIndices := []string{index, indexClone, indexSplit, indexShrink, indexRollover}
	_, err = client.Indices.Delete(
		nil,
		opensearchapi.IndicesDeleteReq{
			Indices: testIndices,
			Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
		},
	)
	require.Nil(t, err)

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
						return client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
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
						resp.Response, err = client.Indices.Exists(nil, opensearchapi.IndicesExistsReq{Indices: []string{index}})
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
						resp.Response, err = failingClient.Indices.Exists(nil, opensearchapi.IndicesExistsReq{Indices: []string{index}})
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
						return client.Indices.Block(nil, opensearchapi.IndicesBlockReq{Indices: []string{index}, Block: "write"})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Block(nil, opensearchapi.IndicesBlockReq{Indices: []string{index}, Block: "write"})
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
						return client.Indices.Analyze(nil, opensearchapi.IndicesAnalyzeReq{Body: opensearchapi.IndicesAnalyzeBody{Text: []string{"test"}, Analyzer: "standard"}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Analyze(nil, opensearchapi.IndicesAnalyzeReq{Body: opensearchapi.IndicesAnalyzeBody{Text: []string{"test"}, Analyzer: "standard"}})
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
						return client.Indices.ClearCache(nil, nil)
					},
				},
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Indices.ClearCache(nil, &opensearchapi.IndicesClearCacheReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.ClearCache(nil, nil)
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
						return client.Indices.Alias.Put(nil, opensearchapi.AliasPutReq{Indices: []string{index}, Alias: alias})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Alias.Put(nil, opensearchapi.AliasPutReq{Indices: []string{index}, Alias: alias})
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
						return client.Indices.Alias.Get(nil, opensearchapi.AliasGetReq{Indices: []string{index}, Alias: []string{alias}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Alias.Get(nil, opensearchapi.AliasGetReq{Indices: []string{index}, Alias: []string{alias}})
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
						resp.Response, err = client.Indices.Alias.Exists(nil, opensearchapi.AliasExistsReq{Indices: []string{index}, Alias: []string{alias}})
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
						resp.Response, err = failingClient.Indices.Alias.Exists(nil, opensearchapi.AliasExistsReq{Indices: []string{index}, Alias: []string{alias}})
						return resp, err
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
						return client.Indices.Rollover(nil, opensearchapi.IndicesRolloverReq{Alias: alias, Index: indexRollover})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Rollover(nil, opensearchapi.IndicesRolloverReq{Alias: alias, Index: indexRollover})
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
						return client.Indices.Alias.Delete(nil, opensearchapi.AliasDeleteReq{Indices: []string{indexRollover}, Alias: []string{alias}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Alias.Delete(nil, opensearchapi.AliasDeleteReq{Indices: []string{indexRollover}, Alias: []string{alias}})
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
						return client.Indices.Mapping.Put(nil, opensearchapi.MappingPutReq{Indices: []string{index}, Body: strings.NewReader(`{"properties":{"test":{"type":"text"}}}`)})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Mapping.Put(nil, opensearchapi.MappingPutReq{Indices: []string{index}, Body: strings.NewReader(`{"properties":{"test":{"type":"text"}}}`)})
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
						return client.Indices.Mapping.Get(nil, &opensearchapi.MappingGetReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Mapping.Get(nil, &opensearchapi.MappingGetReq{Indices: []string{index}})
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
						return client.Indices.Mapping.Field(nil, &opensearchapi.MappingFieldReq{Indices: []string{index}, Fields: []string{"*"}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Mapping.Field(nil, &opensearchapi.MappingFieldReq{Indices: []string{index}, Fields: []string{"*"}})
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
						return client.Indices.Settings.Put(nil, opensearchapi.SettingsPutReq{Indices: []string{index}, Body: strings.NewReader(`{"number_of_replicas":0}`)})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Settings.Put(nil, opensearchapi.SettingsPutReq{Indices: []string{index}, Body: strings.NewReader(`{"number_of_replicas":1}`)})
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
						return client.Indices.Settings.Get(nil, &opensearchapi.SettingsGetReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Settings.Get(nil, &opensearchapi.SettingsGetReq{Indices: []string{index}})
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
						return client.Indices.Flush(nil, &opensearchapi.IndicesFlushReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Flush(nil, &opensearchapi.IndicesFlushReq{Indices: []string{index}})
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
						return client.Indices.Forcemerge(nil, &opensearchapi.IndicesForcemergeReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Forcemerge(nil, &opensearchapi.IndicesForcemergeReq{Indices: []string{index}})
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
						return client.Indices.Clone(nil, opensearchapi.IndicesCloneReq{Index: index, Target: indexClone})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Clone(nil, opensearchapi.IndicesCloneReq{Index: index, Target: indexClone})
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
							nil,
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
						return failingClient.Indices.Split(nil, opensearchapi.IndicesSplitReq{Index: index, Target: indexSplit})
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
							nil,
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
						return failingClient.Indices.Shrink(nil, opensearchapi.IndicesShrinkReq{Index: index, Target: indexClone})
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
						return client.Indices.Get(nil, opensearchapi.IndicesGetReq{Indices: []string{index, indexClone}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Get(nil, opensearchapi.IndicesGetReq{Indices: []string{index, indexClone}})
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
						return client.Indices.Recovery(nil, &opensearchapi.IndicesRecoveryReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Recovery(nil, &opensearchapi.IndicesRecoveryReq{Indices: []string{index}})
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
						return client.Indices.Refresh(nil, &opensearchapi.IndicesRefreshReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Refresh(nil, &opensearchapi.IndicesRefreshReq{Indices: []string{index}})
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
						return client.Indices.Segments(nil, &opensearchapi.IndicesSegmentsReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Segments(nil, &opensearchapi.IndicesSegmentsReq{Indices: []string{index}})
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
						return client.Indices.ShardStores(nil, &opensearchapi.IndicesShardStoresReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.ShardStores(nil, &opensearchapi.IndicesShardStoresReq{Indices: []string{index}})
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
						return client.Indices.Stats(nil, &opensearchapi.IndicesStatsReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Stats(nil, &opensearchapi.IndicesStatsReq{Indices: []string{index}})
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
						return client.Indices.ValidateQuery(nil, opensearchapi.IndicesValidateQueryReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.ValidateQuery(nil, opensearchapi.IndicesValidateQueryReq{Indices: []string{index}})
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
							nil,
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
						return failingClient.Indices.Count(nil, nil)
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
							nil,
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
							nil,
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
						return client.Indices.Resolve(nil, opensearchapi.IndicesResolveReq{Indices: []string{index}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Resolve(nil, opensearchapi.IndicesResolveReq{})
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
						return client.Indices.Close(nil, opensearchapi.IndicesCloseReq{Index: index})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Close(nil, opensearchapi.IndicesCloseReq{Index: index})
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
						return client.Indices.Open(nil, opensearchapi.IndicesOpenReq{Index: index})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Open(nil, opensearchapi.IndicesOpenReq{Index: index})
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
						return client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: testIndices})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: testIndices})
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
						assert.NotNil(t, err)
						assert.NotNil(t, res)
						osapitest.VerifyInspect(t, res.Inspect())
					} else {
						require.Nil(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
					}
				})
			}
		})
	}

	t.Run("ValidateResponse", func(t *testing.T) {
		_, err = client.Indices.Delete(
			nil,
			opensearchapi.IndicesDeleteReq{
				Indices: testIndices,
				Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
			},
		)
		require.Nil(t, err)

		t.Run("Create", func(t *testing.T) {
			resp, err := client.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: index})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Block", func(t *testing.T) {
			resp, err := client.Indices.Block(nil, opensearchapi.IndicesBlockReq{Indices: []string{index}, Block: "write"})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Analyze", func(t *testing.T) {
			resp, err := client.Indices.Analyze(nil, opensearchapi.IndicesAnalyzeReq{Body: opensearchapi.IndicesAnalyzeBody{Text: []string{"test"}, Analyzer: "standard", Explain: true}})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("ClearCache", func(t *testing.T) {
			resp, err := client.Indices.ClearCache(nil, &opensearchapi.IndicesClearCacheReq{Indices: []string{index}})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Alias Put", func(t *testing.T) {
			resp, err := client.Indices.Alias.Put(nil, opensearchapi.AliasPutReq{Indices: []string{index}, Alias: alias})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Alias Get", func(t *testing.T) {
			resp, err := client.Indices.Alias.Get(nil, opensearchapi.AliasGetReq{Indices: []string{index}})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Indices, resp.Inspect().Response)
		})
		t.Run("Rollover", func(t *testing.T) {
			resp, err := client.Indices.Rollover(nil, opensearchapi.IndicesRolloverReq{Alias: alias, Index: indexRollover})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Alias Delete", func(t *testing.T) {
			resp, err := client.Indices.Alias.Delete(nil, opensearchapi.AliasDeleteReq{Indices: []string{indexRollover}, Alias: []string{alias}})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Mapping Put", func(t *testing.T) {
			resp, err := client.Indices.Mapping.Put(nil, opensearchapi.MappingPutReq{Indices: []string{index}, Body: strings.NewReader(`{"properties":{"test":{"type":"text"}}}`)})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Mapping Get", func(t *testing.T) {
			resp, err := client.Indices.Mapping.Get(nil, &opensearchapi.MappingGetReq{Indices: []string{index}})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Indices, resp.Inspect().Response)
		})
		t.Run("Mapping Field", func(t *testing.T) {
			resp, err := client.Indices.Mapping.Field(nil, &opensearchapi.MappingFieldReq{Fields: []string{"*"}})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Indices, resp.Inspect().Response)
		})
		t.Run("Settings Put", func(t *testing.T) {
			resp, err := client.Indices.Settings.Put(nil, opensearchapi.SettingsPutReq{Indices: []string{index}, Body: strings.NewReader(`{"number_of_replicas":1}`)})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Settings Get", func(t *testing.T) {
			resp, err := client.Indices.Settings.Get(nil, &opensearchapi.SettingsGetReq{Indices: []string{index}})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Indices, resp.Inspect().Response)
		})
		t.Run("Flush", func(t *testing.T) {
			resp, err := client.Indices.Flush(nil, &opensearchapi.IndicesFlushReq{Indices: []string{index}})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Forcemerge", func(t *testing.T) {
			resp, err := client.Indices.Forcemerge(nil, &opensearchapi.IndicesForcemergeReq{Indices: []string{index}})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Clone", func(t *testing.T) {
			resp, err := client.Indices.Clone(nil, opensearchapi.IndicesCloneReq{Index: index, Target: indexClone})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Split", func(t *testing.T) {
			resp, err := client.Indices.Split(
				nil,
				opensearchapi.IndicesSplitReq{
					Index:  index,
					Target: indexSplit,
					Body:   strings.NewReader(`{"settings":{"index":{"number_of_shards":2,"number_of_replicas":0}}}`),
				},
			)
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Shrink", func(t *testing.T) {
			resp, err := client.Indices.Shrink(
				nil,
				opensearchapi.IndicesShrinkReq{
					Index:  indexSplit,
					Target: indexShrink,
					Body:   strings.NewReader(`{"settings":{"index":{"number_of_shards":1,"number_of_replicas":0}}}`),
				},
			)
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Get", func(t *testing.T) {
			resp, err := client.Indices.Get(nil, opensearchapi.IndicesGetReq{Indices: []string{index, indexClone}})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Indices, resp.Inspect().Response)
		})
		t.Run("Recovery", func(t *testing.T) {
			resp, err := client.Indices.Recovery(nil, &opensearchapi.IndicesRecoveryReq{Indices: []string{index}})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Indices, resp.Inspect().Response)
		})
		t.Run("Refresh", func(t *testing.T) {
			resp, err := client.Indices.Refresh(nil, &opensearchapi.IndicesRefreshReq{Indices: []string{index}})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Segments", func(t *testing.T) {
			resp, err := client.Indices.Segments(nil, nil)
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("ShardStores", func(t *testing.T) {
			resp, err := client.Indices.ShardStores(nil, nil)
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Stats", func(t *testing.T) {
			resp, err := client.Indices.Stats(nil, nil)
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("ValidateQuery", func(t *testing.T) {
			resp, err := client.Indices.ValidateQuery(
				nil,
				opensearchapi.IndicesValidateQueryReq{
					Indices: []string{index},
					Params: opensearchapi.IndicesValidateQueryParams{
						Rewrite: opensearchapi.ToPointer(true),
					},
				},
			)
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Count", func(t *testing.T) {
			resp, err := client.Indices.Count(nil, nil)
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("FieldCaps", func(t *testing.T) {
			resp, err := client.Indices.FieldCaps(
				nil,
				opensearchapi.IndicesFieldCapsReq{
					Params: opensearchapi.IndicesFieldCapsParams{Fields: []string{"*"}},
				},
			)
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Resolve", func(t *testing.T) {
			resp, err := client.Indices.Resolve(nil, opensearchapi.IndicesResolveReq{Indices: []string{"*"}})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Close", func(t *testing.T) {
			resp, err := client.Indices.Close(nil, opensearchapi.IndicesCloseReq{Index: index})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Open", func(t *testing.T) {
			resp, err := client.Indices.Open(nil, opensearchapi.IndicesOpenReq{Index: index})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
		t.Run("Delete", func(t *testing.T) {
			resp, err := client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: testIndices})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	})
}

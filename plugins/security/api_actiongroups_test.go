// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (plugins || plugin_security)

package security_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v4/plugins/security/internal/test"
)

func TestActiongroupsClient(t *testing.T) {
	testutil.SkipIfNotSecure(t)
	client, err := ossectest.NewClient(t)
	require.NoError(t, err)

	failingClient, err := ossectest.CreateFailingClient()
	require.NoError(t, err)

	type actiongroupsTests struct {
		Name    string
		Results func() (ossectest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []actiongroupsTests
	}{
		{
			Name: "Get",
			Tests: []actiongroupsTests{
				{
					Name: "without request",
					Results: func() (ossectest.Response, error) {
						return client.ActionGroups.Get(nil, nil)
					},
				},
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.ActionGroups.Get(nil, &security.ActionGroupsGetReq{ActionGroup: "write"})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.ActionGroups.Get(nil, nil)
					},
				},
			},
		},
		{
			Name: "Put",
			Tests: []actiongroupsTests{
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.ActionGroups.Put(
							nil,
							security.ActionGroupsPutReq{
								ActionGroup: "test",
								Body: security.ActionGroupsPutBody{
									AllowedActions: []string{"indices:data/read/msearch*", "indices:admin/mapping/put"},
									Type:           opensearch.ToPointer("index"),
									Description:    opensearch.ToPointer("Test"),
								},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.ActionGroups.Put(nil, security.ActionGroupsPutReq{})
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []actiongroupsTests{
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.ActionGroups.Delete(nil, security.ActionGroupsDeleteReq{ActionGroup: "test"})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.ActionGroups.Delete(nil, security.ActionGroupsDeleteReq{})
					},
				},
			},
		},
		{
			Name: "Patch",
			Tests: []actiongroupsTests{
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.ActionGroups.Patch(
							nil,
							security.ActionGroupsPatchReq{
								Body: security.ActionGroupsPatchBody{
									security.ActionGroupsPatchBodyItem{
										OP:   "add",
										Path: "/test",
										Value: security.ActionGroupsPutBody{
											AllowedActions: []string{"indices:data/read/msearch*", "indices:admin/mapping/put"},
										},
									},
								},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.ActionGroups.Patch(t.Context(), security.ActionGroupsPatchReq{})
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
						ossectest.VerifyInspect(t, res.Inspect())
					} else {
						require.NoError(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
						if value.Name != "Get" {
							testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
						}
					}
				})
			}
		})
	}
	t.Run("ValidateResponse", func(t *testing.T) {
		t.Run("Get", func(t *testing.T) {
			resp, err := client.ActionGroups.Get(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Groups, resp.Inspect().Response)
		})
	})
}

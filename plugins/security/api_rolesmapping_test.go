// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (plugins || plugin_security)

package security_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v4/plugins/security/internal/test"
)

func TestSecurityRolesMappingClient(t *testing.T) {
	testutil.SkipIfNotSecure(t)
	client, err := ossectest.NewClient(t)
	require.NoError(t, err)

	failingClient, err := ossectest.CreateFailingClient()
	require.NoError(t, err)

	testRole := "test_role"
	client.Roles.Put(
		t.Context(),
		security.RolesPutReq{
			Role: testRole,
			Body: security.RolesPutBody{
				Description:        "Test",
				ClusterPermissions: []string{"cluster_monitor"},
				IndexPermissions: []security.RolesIndexPermission{{
					IndexPatterns:  []string{"*"},
					AllowedActions: []string{"indices_monitor"},
				}},
			},
		},
	)
	t.Cleanup(func() {
		client.Roles.Delete(t.Context(), security.RolesDeleteReq{Role: testRole})
	})

	type rolesmappingTests struct {
		Name    string
		Results func() (ossectest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []rolesmappingTests
	}{
		{
			Name: "Put",
			Tests: []rolesmappingTests{
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.RolesMapping.Put(
							t.Context(),
							security.RolesMappingPutReq{
								Role: testRole,
								Body: security.RolesMappingPutBody{
									Description:  "Test",
									Users:        []string{"test"},
									BackendRoles: []string{"test"},
								},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.RolesMapping.Put(t.Context(), security.RolesMappingPutReq{})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []rolesmappingTests{
				{
					Name: "without request",
					Results: func() (ossectest.Response, error) {
						return client.RolesMapping.Get(t.Context(), nil)
					},
				},
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.RolesMapping.Get(t.Context(), &security.RolesMappingGetReq{Role: testRole})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.RolesMapping.Get(t.Context(), nil)
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []rolesmappingTests{
				{
					Name: "without request",
					Results: func() (ossectest.Response, error) {
						return client.RolesMapping.Delete(t.Context(), security.RolesMappingDeleteReq{Role: testRole})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.RolesMapping.Delete(t.Context(), security.RolesMappingDeleteReq{Role: testRole})
					},
				},
			},
		},
		{
			Name: "Patch",
			Tests: []rolesmappingTests{
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.RolesMapping.Patch(
							t.Context(),
							security.RolesMappingPatchReq{
								Body: security.RolesMappingPatchBody{
									security.RolesMappingPatchBodyItem{
										OP:   "add",
										Path: "/test",
										Value: security.RolesMappingPutBody{
											Description:  "Test",
											Users:        []string{"test"},
											BackendRoles: []string{"test"},
										},
									},
									security.RolesMappingPatchBodyItem{
										OP:   "remove",
										Path: "/test",
									},
								},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.RolesMapping.Patch(t.Context(), security.RolesMappingPatchReq{})
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
						if err != nil {
							fmt.Println(err)
						}
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
			resp, err := client.RolesMapping.Get(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.RolesMapping, resp.Inspect().Response)
		})
	})
}

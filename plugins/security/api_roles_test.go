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

func TestSecurityRolesClient(t *testing.T) {
	testutil.SkipIfNotSecure(t)
	client, err := ossectest.NewClient(t)
	require.NoError(t, err)

	failingClient, err := ossectest.CreateFailingClient()
	require.NoError(t, err)

	testRole := "test_role"

	type rolesTests struct {
		Name    string
		Results func() (ossectest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []rolesTests
	}{
		{
			Name: "Put",
			Tests: []rolesTests{
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.Roles.Put(
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
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.Roles.Put(t.Context(), security.RolesPutReq{})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []rolesTests{
				{
					Name: "without request",
					Results: func() (ossectest.Response, error) {
						return client.Roles.Get(t.Context(), nil)
					},
				},
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.Roles.Get(t.Context(), &security.RolesGetReq{Role: testRole})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.Roles.Get(t.Context(), nil)
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []rolesTests{
				{
					Name: "without request",
					Results: func() (ossectest.Response, error) {
						return client.Roles.Delete(t.Context(), security.RolesDeleteReq{Role: testRole})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.Roles.Delete(t.Context(), security.RolesDeleteReq{Role: testRole})
					},
				},
			},
		},
		{
			Name: "Patch",
			Tests: []rolesTests{
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.Roles.Patch(
							t.Context(),
							security.RolesPatchReq{
								Body: security.RolesPatchBody{
									security.RolesPatchBodyItem{
										OP:   "add",
										Path: "/test",
										Value: security.RolesPutBody{
											Description:        "Test",
											ClusterPermissions: []string{"cluster_monitor"},
											IndexPermissions: []security.RolesIndexPermission{{
												IndexPatterns:  []string{"*"},
												AllowedActions: []string{"indices_monitor"},
											}},
										},
									},
									security.RolesPatchBodyItem{
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
						return failingClient.Roles.Patch(t.Context(), security.RolesPatchReq{})
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
			resp, err := client.Roles.Get(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Roles, resp.Inspect().Response)
		})
	})
}

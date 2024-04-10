// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration

package security_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v3/internal/test"
	"github.com/opensearch-project/opensearch-go/v3/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v3/plugins/security/internal/test"
)

func TestRolesClient(t *testing.T) {
	ostest.SkipIfNotSecure(t)
	client, err := ossectest.NewClient()
	require.Nil(t, err)

	failingClient, err := ossectest.CreateFailingClient()
	require.Nil(t, err)

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
							nil,
							security.RolesPutReq{
								Role: testRole,
								Body: security.RolesPutBody{
									Description:        "Test",
									ClusterPermissions: []string{"cluster_monitor"},
									IndexPermissions:   []security.RolesIndexPermission{security.RolesIndexPermission{IndexPatterns: []string{"*"}, AllowedActions: []string{"indices_monitor"}}},
								},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.Roles.Put(nil, security.RolesPutReq{})
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
						return client.Roles.Get(nil, nil)
					},
				},
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.Roles.Get(nil, &security.RolesGetReq{Role: testRole})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.Roles.Get(nil, nil)
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
						return client.Roles.Delete(nil, security.RolesDeleteReq{Role: testRole})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.Roles.Delete(nil, security.RolesDeleteReq{Role: testRole})
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
							nil,
							security.RolesPatchReq{
								Body: security.RolesPatchBody{
									security.RolesPatchBodyItem{
										OP:   "add",
										Path: "/test",
										Value: security.RolesPutBody{
											Description:        "Test",
											ClusterPermissions: []string{"cluster_monitor"},
											IndexPermissions:   []security.RolesIndexPermission{security.RolesIndexPermission{IndexPatterns: []string{"*"}, AllowedActions: []string{"indices_monitor"}}},
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
						return failingClient.Roles.Patch(nil, security.RolesPatchReq{})
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
						ossectest.VerifyInspect(t, res.Inspect())
					} else {
						if err != nil {
							fmt.Println(err)
						}
						require.Nil(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
						if value.Name != "Get" {
							ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
						}
					}
				})
			}
		})
	}
	t.Run("ValidateResponse", func(t *testing.T) {
		t.Run("Get", func(t *testing.T) {
			resp, err := client.Roles.Get(nil, nil)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Roles, resp.Inspect().Response)
		})
	})
}

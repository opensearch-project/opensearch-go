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

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v4/plugins/security/internal/test"
)

func TestInternalUsersClient(t *testing.T) {
	ostest.SkipIfNotSecure(t)
	client, err := ossectest.NewClient()
	require.Nil(t, err)

	failingClient, err := ossectest.CreateFailingClient()
	require.Nil(t, err)

	testUser := "test_user"

	type internalusersTests struct {
		Name    string
		Results func() (ossectest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []internalusersTests
	}{
		{
			Name: "Put",
			Tests: []internalusersTests{
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.InternalUsers.Put(
							t.Context(),
							security.InternalUsersPutReq{
								User: testUser,
								Body: security.InternalUsersPutBody{
									Password: "myStrongPassword123!",
								},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.InternalUsers.Put(t.Context(), security.InternalUsersPutReq{})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []internalusersTests{
				{
					Name: "without request",
					Results: func() (ossectest.Response, error) {
						return client.InternalUsers.Get(t.Context(), nil)
					},
				},
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.InternalUsers.Get(t.Context(), &security.InternalUsersGetReq{User: testUser})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.InternalUsers.Get(t.Context(), nil)
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []internalusersTests{
				{
					Name: "without request",
					Results: func() (ossectest.Response, error) {
						return client.InternalUsers.Delete(t.Context(), security.InternalUsersDeleteReq{User: testUser})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.InternalUsers.Delete(t.Context(), security.InternalUsersDeleteReq{User: testUser})
					},
				},
			},
		},
		{
			Name: "Patch",
			Tests: []internalusersTests{
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.InternalUsers.Patch(
							t.Context(),
							security.InternalUsersPatchReq{
								Body: security.InternalUsersPatchBody{
									security.InternalUsersPatchBodyItem{
										OP:   "add",
										Path: "/test",
										Value: security.InternalUsersPutBody{
											Password: "myStrongPassword123!",
										},
									},
									security.InternalUsersPatchBodyItem{
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
						return failingClient.InternalUsers.Patch(t.Context(), security.InternalUsersPatchReq{})
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
			resp, err := client.InternalUsers.Get(t.Context(), nil)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Users, resp.Inspect().Response)
		})
	})
}

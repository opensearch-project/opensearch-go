// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (plugins || plugin_security)

package security_test

import (
	"github.com/opensearch-project/opensearch-go/v4"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v4/plugins/security/internal/test"
)

func TestAccountClient(t *testing.T) {
	ostest.SkipIfNotSecure(t)
	client, err := ossectest.NewClient()
	require.Nil(t, err)

	failingClient, err := ossectest.CreateFailingClient()
	require.Nil(t, err)

	testUser := "testUser"
	t.Cleanup(func() { client.InternalUsers.Delete(nil, security.InternalUsersDeleteReq{User: testUser}) })

	type accountTests struct {
		Name    string
		Results func() (any, *opensearch.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []accountTests
	}{
		{
			Name: "Get",
			Tests: []accountTests{
				{
					Name: "without request",
					Results: func() (any, *opensearch.Response, error) {
						return client.Account.Get(nil, nil)
					},
				},
				{
					Name: "inspect",
					Results: func() (any, *opensearch.Response, error) {
						return failingClient.Account.Get(nil, nil)
					},
				},
			},
		},
		{
			Name: "Put",
			Tests: []accountTests{
				{
					Name: "with request",
					Results: func() (any, *opensearch.Response, error) {
						// Get new client config
						config, err := ossectest.ClientConfig()
						if err != nil {
							return nil, nil, err
						}

						// Set password to a "strong" password
						config.Client.Password = "Str0ngP4ss123!"
						config.Client.Username = testUser

						// Create the test user
						_, _, err = client.InternalUsers.Put(
							nil,
							security.InternalUsersPutReq{
								User: config.Client.Username,
								Body: security.InternalUsersPutBody{
									Password: config.Client.Password,
								},
							},
						)
						if err != nil {
							return nil, nil, err
						}

						// Create a new client with the test user
						usrClient, err := security.NewClient(*config)
						if err != nil {
							return nil, nil, err
						}

						// Run the change password request we want to test
						return usrClient.Account.Put(
							nil,
							security.AccountPutReq{
								Body: security.AccountPutBody{
									CurrentPassword: config.Client.Password,
									Password:        "myStrongPassword123!",
								},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (any, *opensearch.Response, error) {
						return failingClient.Account.Put(nil, security.AccountPutReq{})
					},
				},
			},
		},
	}
	for _, value := range testCases {
		t.Run(value.Name, func(t *testing.T) {
			for _, testCase := range value.Tests {
				t.Run(testCase.Name, func(t *testing.T) {
					res, httpResp, err := testCase.Results()
					if testCase.Name == "inspect" {
						assert.NotNil(t, err)
						assert.NotNil(t, res)
						ossectest.VerifyResponse(t, httpResp)
					} else {
						require.Nil(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, httpResp)
						ostest.CompareRawJSONwithParsedJSON(t, res, httpResp)
					}
				})
			}
		})
	}
}

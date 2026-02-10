// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (plugins || plugin_security)

package security_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v4/plugins/security/internal/test"
)

func TestActiongroupsClient(t *testing.T) {
	ostest.SkipIfNotSecure(t)
	client, err := ossectest.NewClient()
	require.Nil(t, err)

	failingClient, err := ossectest.CreateFailingClient()
	require.Nil(t, err)

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
						return client.ActionGroups.Get(t.Context(), nil)
					},
				},
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.ActionGroups.Get(t.Context(), &security.ActionGroupsGetReq{ActionGroup: "write"})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.ActionGroups.Get(t.Context(), nil)
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
						// Retry logic for transient timeout errors from security plugin
						const maxRetries = 3
						var lastErr error
						var resp security.ActionGroupsPutResp

						for attempt := range maxRetries {
							resp, lastErr = client.ActionGroups.Put(
								t.Context(),
								security.ActionGroupsPutReq{
									ActionGroup: "test",
									Body: security.ActionGroupsPutBody{
										AllowedActions: []string{"indices:data/read/msearch*", "indices:admin/mapping/put"},
										Type:           opensearch.ToPointer("index"),
										Description:    opensearch.ToPointer("Test"),
									},
								},
							)

							// Check if error is a transient timeout
							if lastErr == nil {
								return resp, nil
							}

							// Check if it's a timeout error worth retrying
							if structErr, ok := lastErr.(*opensearch.StructError); ok {
								if structErr.Status == 500 && strings.Contains(structErr.Err.Reason, "TimeoutException") {
									if attempt < maxRetries-1 {
										t.Logf("Timeout on attempt %d, retrying...", attempt+1)
										time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
										continue
									}
								}
							}

							// Non-timeout error, return immediately
							return resp, lastErr
						}

						return resp, lastErr
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.ActionGroups.Put(t.Context(), security.ActionGroupsPutReq{})
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
						return client.ActionGroups.Delete(t.Context(), security.ActionGroupsDeleteReq{ActionGroup: "test"})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.ActionGroups.Delete(t.Context(), security.ActionGroupsDeleteReq{})
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
							t.Context(),
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
			resp, err := client.ActionGroups.Get(t.Context(), nil)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Groups, resp.Inspect().Response)
		})
	})
}

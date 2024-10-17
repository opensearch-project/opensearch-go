// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (plugins || plugin_index_management)

package ism_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/plugins/ism"
	osismtest "github.com/opensearch-project/opensearch-go/v4/plugins/ism/internal/test"
)

func TestPoliciesClient(t *testing.T) {
	client, err := osismtest.NewClient()
	require.Nil(t, err)

	osClient, err := ostest.NewClient()
	require.Nil(t, err)

	failingClient, err := osismtest.CreateFailingClient()
	require.Nil(t, err)

	testPolicy := "test"
	testPolicyChannel := "test_channel"
	testPolicyAlias := "test_alias"
	t.Cleanup(func() {
		client.Policies.Delete(nil, ism.PoliciesDeleteReq{Policy: testPolicyChannel})
		client.Policies.Delete(nil, ism.PoliciesDeleteReq{Policy: testPolicyAlias})
	})

	var putResp ism.PoliciesPutResp
	var httpResp *opensearch.Response

	type policiesTests struct {
		Name    string
		Results func(*testing.T) (any, *opensearch.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []policiesTests
	}{
		{
			Name: "Put",
			Tests: []policiesTests{
				{
					Name: "Create",
					Results: func(t *testing.T) (any, *opensearch.Response, error) {
						putResp, httpResp, err = client.Policies.Put(
							nil,
							ism.PoliciesPutReq{
								Policy: testPolicy,
								Body: ism.PoliciesPutBody{
									Policy: ism.PolicyBody{
										Description: "test",
										ErrorNotification: &ism.PolicyErrorNotification{
											Destination: &ism.NotificationDestination{
												CustomWebhook: &ism.NotificationDestinationCustomWebhook{
													Host:         "exmaple.com",
													Scheme:       "https",
													Path:         "/test",
													Username:     "test",
													Password:     "test",
													HeaderParams: map[string]string{"test": "2"},
													QueryParams:  map[string]string{"test": "2"},
													Port:         443,
													URL:          "example.com",
												},
											},
											MessageTemplate: ism.NotificationMessageTemplate{
												Source: "The index {{ctx.index}} failed during policy execution.",
											},
										},
										DefaultState: "transition",
										States: []ism.PolicyState{
											ism.PolicyState{
												Name: "allocation",
												Actions: []ism.PolicyStateAction{
													ism.PolicyStateAction{
														Allocation: &ism.PolicyStateAllocation{
															Require: map[string]string{"temp": "warm"},
															Include: map[string]string{"test": "warm"},
															Exclude: map[string]string{"test2": "warm"},
															WaitFor: opensearch.ToPointer(true),
														},
													},
												},
												Transitions: &[]ism.PolicyStateTransition{
													ism.PolicyStateTransition{
														StateName: "transition",
													},
												},
											},
											ism.PolicyState{
												Name: "transition",
												Actions: []ism.PolicyStateAction{
													ism.PolicyStateAction{
														Close: &ism.PolicyStateClose{},
													},
												},
												Transitions: &[]ism.PolicyStateTransition{
													ism.PolicyStateTransition{
														StateName: "delete",
													},
												},
											},
											ism.PolicyState{
												Name: "delete",
												Actions: []ism.PolicyStateAction{
													ism.PolicyStateAction{
														Delete: &ism.PolicyStateDelete{},
													},
												},
											},
										},
										Template: []ism.Template{
											ism.Template{
												IndexPatterns: []string{"test"},
												Priority:      20,
											},
										},
									},
								},
							},
						)
						return putResp, httpResp, err
					},
				},
				{
					Name: "Create with Channel",
					Results: func(t *testing.T) (any, *opensearch.Response, error) {
						ostest.SkipIfBelowVersion(t, osClient, 2, 0, "policy with error notification channel")
						return client.Policies.Put(
							nil,
							ism.PoliciesPutReq{
								Policy: testPolicyChannel,
								Body: ism.PoliciesPutBody{
									Policy: ism.PolicyBody{
										Description: "test",
										ErrorNotification: &ism.PolicyErrorNotification{
											Channel: &ism.NotificationChannel{
												ID: "test",
											},
											MessageTemplate: ism.NotificationMessageTemplate{
												Source: "The index {{ctx.index}} failed during policy execution.",
											},
										},
										DefaultState: "delete",
										States: []ism.PolicyState{
											ism.PolicyState{
												Name: "delete",
												Actions: []ism.PolicyStateAction{
													ism.PolicyStateAction{
														Delete: &ism.PolicyStateDelete{},
													},
												},
											},
										},
										Template: []ism.Template{
											ism.Template{
												IndexPatterns: []string{testPolicyChannel},
												Priority:      21,
											},
										},
									},
								},
							},
						)
					},
				},
				{
					Name: "Create with Alias",
					Results: func(t *testing.T) (any, *opensearch.Response, error) {
						ostest.SkipIfBelowVersion(t, osClient, 2, 4, "policy with alias action")
						return client.Policies.Put(
							nil,
							ism.PoliciesPutReq{
								Policy: testPolicyAlias,
								Body: ism.PoliciesPutBody{
									Policy: ism.PolicyBody{
										Description:  "test",
										DefaultState: "alias",
										States: []ism.PolicyState{
											ism.PolicyState{
												Name: "alias",
												Actions: []ism.PolicyStateAction{
													ism.PolicyStateAction{
														Alias: &ism.PolicyStateAlias{
															Actions: []ism.PolicyStateAliasAction{
																ism.PolicyStateAliasAction{
																	Add: &ism.PolicyStateAliasName{Aliases: []string{"alias-test"}},
																},
																ism.PolicyStateAliasAction{
																	Remove: &ism.PolicyStateAliasName{Aliases: []string{"alias-test"}},
																},
															},
														},
													},
												},
											},
										},
										Template: []ism.Template{
											ism.Template{
												IndexPatterns: []string{testPolicyAlias},
												Priority:      21,
											},
										},
									},
								},
							},
						)
					},
				},
				{
					Name: "Update",
					Results: func(t *testing.T) (any, *opensearch.Response, error) {
						putResp.Policy.Policy.ErrorNotification.Destination.CustomWebhook = nil
						putResp.Policy.Policy.ErrorNotification.Destination.Slack = &ism.NotificationDestinationURL{URL: "https://example.com"}
						return client.Policies.Put(
							nil,
							ism.PoliciesPutReq{
								Policy: testPolicy,
								Params: ism.PoliciesPutParams{IfSeqNo: opensearch.ToPointer(putResp.SeqNo), IfPrimaryTerm: opensearch.ToPointer(putResp.PrimaryTerm)},
								Body: ism.PoliciesPutBody{
									Policy: putResp.Policy.Policy,
								},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func(t *testing.T) (any, *opensearch.Response, error) {
						return failingClient.Policies.Put(nil, ism.PoliciesPutReq{})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []policiesTests{
				{
					Name: "without request",
					Results: func(t *testing.T) (any, *opensearch.Response, error) {
						return client.Policies.Get(nil, nil)
					},
				},
				{
					Name: "with request",
					Results: func(t *testing.T) (any, *opensearch.Response, error) {
						return client.Policies.Get(nil, &ism.PoliciesGetReq{Policy: testPolicy})
					},
				},
				{
					Name: "inspect",
					Results: func(t *testing.T) (any, *opensearch.Response, error) {
						return failingClient.Policies.Get(nil, nil)
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []policiesTests{
				{
					Name: "with request",
					Results: func(t *testing.T) (any, *opensearch.Response, error) {
						return client.Policies.Delete(nil, ism.PoliciesDeleteReq{Policy: testPolicy})
					},
				},
				{
					Name: "inspect",
					Results: func(t *testing.T) (any, *opensearch.Response, error) {
						return failingClient.Policies.Delete(nil, ism.PoliciesDeleteReq{})
					},
				},
			},
		},
	}
	for _, value := range testCases {
		t.Run(value.Name, func(t *testing.T) {
			for _, testCase := range value.Tests {
				t.Run(testCase.Name, func(t *testing.T) {
					res, httpResp, err := testCase.Results(t)
					if testCase.Name == "inspect" {
						assert.NotNil(t, err)
						assert.NotNil(t, res)
						osismtest.VerifyResponse(t, httpResp)
					} else {
						require.NoError(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, httpResp)
						ostest.CompareRawJSONwithParsedJSON(t, res, httpResp)
					}
				})
			}
		})
	}
}

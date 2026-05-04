// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (plugins || plugin_index_management)

package ism_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v4/plugins/ism"
	osismtest "github.com/opensearch-project/opensearch-go/v4/plugins/ism/internal/test"
)

func TestPoliciesClient(t *testing.T) {
	client, err := osismtest.NewClient(t)
	require.NoError(t, err)

	osClient, err := testutil.NewClient(t)
	require.NoError(t, err)

	failingClient, err := osismtest.CreateFailingClient(t)
	require.NoError(t, err)

	testPolicy := testutil.MustUniqueString(t, "test")
	testPolicyChannel := testutil.MustUniqueString(t, "test-channel")
	testPolicyAlias := testutil.MustUniqueString(t, "test-alias")
	t.Cleanup(func() {
		client.Policies.Delete(context.Background(), ism.PoliciesDeleteReq{Policy: testPolicy})
		client.Policies.Delete(context.Background(), ism.PoliciesDeleteReq{Policy: testPolicyChannel})
		client.Policies.Delete(context.Background(), ism.PoliciesDeleteReq{Policy: testPolicyAlias})
	})

	type policiesTests struct {
		Name    string
		Results func(*testing.T) (osismtest.Response, error)
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
					Results: func(t *testing.T) (osismtest.Response, error) {
						t.Helper()
						return client.Policies.Put(
							t.Context(),
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
											{
												Name: "allocation",
												Actions: []ism.PolicyStateAction{
													{
														Allocation: &ism.PolicyStateAllocation{
															Require: map[string]string{"temp": "warm"},
															Include: map[string]string{"test": "warm"},
															Exclude: map[string]string{"test2": "warm"},
															WaitFor: opensearch.ToPointer(true),
														},
													},
												},
												Transitions: &[]ism.PolicyStateTransition{
													{
														StateName: "transition",
													},
												},
											},
											{
												Name: "transition",
												Actions: []ism.PolicyStateAction{
													{
														Close: &ism.PolicyStateClose{},
													},
												},
												Transitions: &[]ism.PolicyStateTransition{
													{
														StateName: "delete",
													},
												},
											},
											{
												Name: "delete",
												Actions: []ism.PolicyStateAction{
													{
														Delete: &ism.PolicyStateDelete{},
													},
												},
											},
										},
										Template: []ism.Template{
											{
												IndexPatterns: []string{testPolicy},
												Priority:      20,
											},
										},
									},
								},
							},
						)
					},
				},
				{
					Name: "Create with Channel",
					Results: func(t *testing.T) (osismtest.Response, error) {
						t.Helper()
						testutil.SkipIfVersion(t, osClient, "<", "2.0", "policy with error notification channel")
						return client.Policies.Put(
							t.Context(),
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
											{
												Name: "delete",
												Actions: []ism.PolicyStateAction{
													{
														Delete: &ism.PolicyStateDelete{},
													},
												},
											},
										},
										Template: []ism.Template{
											{
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
					Results: func(t *testing.T) (osismtest.Response, error) {
						t.Helper()
						testutil.SkipIfVersion(t, osClient, "<", "2.4", "policy with alias action")
						return client.Policies.Put(
							t.Context(),
							ism.PoliciesPutReq{
								Policy: testPolicyAlias,
								Body: ism.PoliciesPutBody{
									Policy: ism.PolicyBody{
										Description:  "test",
										DefaultState: "alias",
										States: []ism.PolicyState{
											{
												Name: "alias",
												Actions: []ism.PolicyStateAction{
													{
														Alias: &ism.PolicyStateAlias{
															Actions: []ism.PolicyStateAliasAction{
																{
																	Add: &ism.PolicyStateAliasName{Aliases: []string{testPolicyAlias + "-alias"}},
																},
																{
																	Remove: &ism.PolicyStateAliasName{Aliases: []string{testPolicyAlias + "-alias"}},
																},
															},
														},
													},
												},
											},
										},
										Template: []ism.Template{
											{
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
					Results: func(t *testing.T) (osismtest.Response, error) {
						t.Helper()
						// Re-fetch the policy to get current seqNo/primaryTerm.
						// The policy may have been modified by concurrent tests
						// or background operations since the initial Create.
						getResp, err := client.Policies.Get(t.Context(), &ism.PoliciesGetReq{Policy: testPolicy})
						if err != nil {
							return getResp, err
						}
						if getResp.Policy == nil || getResp.Policy.ErrorNotification == nil || getResp.Policy.ErrorNotification.Destination == nil {
							t.Skip("Skipping Update test - policy does not have expected ErrorNotification/Destination")
						}
						policy := *getResp.Policy
						policy.ErrorNotification.Destination.CustomWebhook = nil
						policy.ErrorNotification.Destination.Slack = &ism.NotificationDestinationURL{URL: "https://example.com"}
						return client.Policies.Put(
							t.Context(),
							ism.PoliciesPutReq{
								Policy: testPolicy,
								Params: ism.PoliciesPutParams{
									IfSeqNo:       getResp.SeqNo,
									IfPrimaryTerm: getResp.PrimaryTerm,
								},
								Body: ism.PoliciesPutBody{
									Policy: policy,
								},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func(t *testing.T) (osismtest.Response, error) {
						t.Helper()
						return failingClient.Policies.Put(t.Context(), ism.PoliciesPutReq{Policy: "test"})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []policiesTests{
				{
					Name: "without request",
					Results: func(t *testing.T) (osismtest.Response, error) {
						t.Helper()
						return client.Policies.Get(t.Context(), nil)
					},
				},
				{
					Name: "with request",
					Results: func(t *testing.T) (osismtest.Response, error) {
						t.Helper()
						return client.Policies.Get(t.Context(), &ism.PoliciesGetReq{Policy: testPolicy})
					},
				},
				{
					Name: "inspect",
					Results: func(t *testing.T) (osismtest.Response, error) {
						t.Helper()
						return failingClient.Policies.Get(t.Context(), nil)
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []policiesTests{
				{
					Name: "with request",
					Results: func(t *testing.T) (osismtest.Response, error) {
						t.Helper()
						return client.Policies.Delete(t.Context(), ism.PoliciesDeleteReq{Policy: testPolicy})
					},
				},
				{
					Name: "inspect",
					Results: func(t *testing.T) (osismtest.Response, error) {
						t.Helper()
						return failingClient.Policies.Delete(t.Context(), ism.PoliciesDeleteReq{Policy: "test"})
					},
				},
			},
		},
	}
	for _, value := range testCases {
		t.Run(value.Name, func(t *testing.T) {
			for _, testCase := range value.Tests {
				t.Run(testCase.Name, func(t *testing.T) {
					res, err := testCase.Results(t)
					if testCase.Name == "inspect" {
						require.Error(t, err)
						assert.NotNil(t, res)
						osismtest.VerifyInspect(t, res.Inspect())
					} else {
						require.NoError(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
						testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
					}
				})
			}
		})
	}
}

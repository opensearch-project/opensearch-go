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

	failingClient, err := osismtest.CreateFailingClient()
	require.Nil(t, err)

	var putResp ism.PoliciesPutResp

	type policiesTests struct {
		Name    string
		Results func() (osismtest.Response, error)
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
					Results: func() (osismtest.Response, error) {
						putResp, err = client.Policies.Put(
							nil,
							ism.PoliciesPutReq{
								Policy: "test",
								Body: ism.PoliciesPutBody{
									Policy: ism.PolicyBody{
										Description: "test",
										ErrorNotification: &ism.PolicyErrorNotification{
											Destination: ism.NotificationDestination{
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
												Name: "delete",
												Actions: []ism.PolicyStateAction{
													ism.PolicyStateAction{
														Delete: &ism.PolicyStateDelete{},
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
										},
										Template: []ism.Template{
											ism.Template{
												IndexPatterns: []string{"*test*"},
												Priority:      20,
											},
										},
									},
								},
							},
						)
						return putResp, err
					},
				},
				{
					Name: "Update",
					Results: func() (osismtest.Response, error) {
						putResp.Policy.Policy.ErrorNotification.Destination.CustomWebhook = nil
						putResp.Policy.Policy.ErrorNotification.Destination.Slack = &ism.NotificationDestinationURL{URL: "https://example.com"}
						return client.Policies.Put(
							nil,
							ism.PoliciesPutReq{
								Policy: "test",
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
					Results: func() (osismtest.Response, error) {
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
					Results: func() (osismtest.Response, error) {
						return client.Policies.Get(nil, nil)
					},
				},
				{
					Name: "with request",
					Results: func() (osismtest.Response, error) {
						return client.Policies.Get(nil, &ism.PoliciesGetReq{Policy: "test"})
					},
				},
				{
					Name: "inspect",
					Results: func() (osismtest.Response, error) {
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
					Results: func() (osismtest.Response, error) {
						return client.Policies.Delete(nil, ism.PoliciesDeleteReq{Policy: "test"})
					},
				},
				{
					Name: "inspect",
					Results: func() (osismtest.Response, error) {
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
					res, err := testCase.Results()
					if testCase.Name == "inspect" {
						assert.NotNil(t, err)
						assert.NotNil(t, res)
						osismtest.VerifyInspect(t, res.Inspect())
					} else {
						require.NoError(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
						ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
					}
				})
			}
		})
	}
}

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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v4/plugins/ism"
	osismtest "github.com/opensearch-project/opensearch-go/v4/plugins/ism/internal/test"
)

func TestClient(t *testing.T) {
	t.Parallel()
	client, err := osismtest.NewClient()
	require.Nil(t, err)

	osClient, err := ostest.NewClient()
	require.Nil(t, err)

	failingClient, err := osismtest.CreateFailingClient()
	require.Nil(t, err)

	testPolicy := "testPolicy"
	testIndex := []string{"test_policy"}

	t.Cleanup(func() { client.Policies.Delete(nil, ism.PoliciesDeleteReq{Policy: testPolicy}) })
	_, err = client.Policies.Put(
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
					DefaultState: "test",
					States: []ism.PolicyState{
						ism.PolicyState{
							Name: "test",
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
							Priority:      22,
						},
					},
				},
			},
		},
	)
	require.Nil(t, err)

	t.Cleanup(func() { osClient.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: testIndex}) })
	_, err = osClient.Indices.Create(nil, opensearchapi.IndicesCreateReq{Index: testIndex[0]})
	require.Nil(t, err)

	type clientTests struct {
		Name    string
		Results func() (osismtest.Response, error)
	}

	waitFor := func() error {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			resp, err := client.Explain(nil, &ism.ExplainReq{Indices: testIndex})
			if err != nil {
				return err
			}
			if resp.Indices[testIndex[0]].Info != nil && resp.Indices[testIndex[0]].Info.Message != "" {
				return nil
			}
		}
		return nil
	}
	testCases := []struct {
		Name  string
		Tests []clientTests
	}{
		{
			Name: "Add",
			Tests: []clientTests{
				{
					Name: "okay",
					Results: func() (osismtest.Response, error) {
						return client.Add(nil, ism.AddReq{Indices: testIndex, Body: ism.AddBody{PolicyID: testPolicy}})
					},
				},
				{
					Name: "failure",
					Results: func() (osismtest.Response, error) {
						return client.Add(nil, ism.AddReq{Indices: testIndex, Body: ism.AddBody{PolicyID: testPolicy}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osismtest.Response, error) {
						return failingClient.Add(nil, ism.AddReq{})
					},
				},
			},
		},
		{
			Name: "Explain",
			Tests: []clientTests{
				{
					Name: "without body",
					Results: func() (osismtest.Response, error) {
						return client.Explain(nil, &ism.ExplainReq{Indices: testIndex})
					},
				},
				{
					Name: "inspect",
					Results: func() (osismtest.Response, error) {
						return failingClient.Explain(nil, &ism.ExplainReq{})
					},
				},
			},
		},
		{
			Name: "Change",
			Tests: []clientTests{
				{
					Name: "with request",
					Results: func() (osismtest.Response, error) {
						return client.Change(nil, ism.ChangeReq{Indices: testIndex, Body: ism.ChangeBody{PolicyID: testPolicy, State: "delete"}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osismtest.Response, error) {
						return failingClient.Change(nil, ism.ChangeReq{})
					},
				},
			},
		},
		{
			Name: "Retry",
			Tests: []clientTests{
				{
					Name: "without body",
					Results: func() (osismtest.Response, error) {
						return client.Retry(nil, ism.RetryReq{Indices: testIndex})
					},
				},
				{
					Name: "with body",
					Results: func() (osismtest.Response, error) {
						return client.Retry(nil, ism.RetryReq{Indices: testIndex, Body: &ism.RetryBody{State: "test"}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osismtest.Response, error) {
						return failingClient.Retry(nil, ism.RetryReq{})
					},
				},
			},
		},
		{
			Name: "Remove",
			Tests: []clientTests{
				{
					Name: "with request",
					Results: func() (osismtest.Response, error) {
						return client.Remove(nil, ism.RemoveReq{Indices: testIndex})
					},
				},
				{
					Name: "inspect",
					Results: func() (osismtest.Response, error) {
						return failingClient.Remove(nil, ism.RemoveReq{})
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
						if value.Name != "Explain" {
							ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
						}
						if value.Name == "Add" && testCase.Name == "failure" {
							err = waitFor()
							assert.NoError(t, err)
						}
					}
				})
			}
		})
	}
	t.Run("ValidateResponse", func(t *testing.T) {
		t.Run("Explain", func(t *testing.T) {
			resp, err := client.Explain(nil, &ism.ExplainReq{Indices: testIndex})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, &resp, resp.Inspect().Response)
		})
		t.Run("Explain with validate_action", func(t *testing.T) {
			ostest.SkipIfBelowVersion(t, osClient, 2, 4, "Explain with validate_action")
			resp, err := client.Explain(nil, &ism.ExplainReq{Indices: testIndex, Params: ism.ExplainParams{ShowPolicy: true, ValidateAction: true}})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, &resp, resp.Inspect().Response)
		})
		t.Run("Explain with show_policy", func(t *testing.T) {
			ostest.SkipIfBelowVersion(t, osClient, 1, 3, "Explain with show_policy")
			resp, err := client.Explain(nil, &ism.ExplainReq{Indices: testIndex, Params: ism.ExplainParams{ShowPolicy: true}})
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, &resp, resp.Inspect().Response)
		})
	})

	t.Run("Put Policy with Transitions Conditions", func(t *testing.T) {
		testRetentionPolicy := "testRetentionPolicy"
		t.Cleanup(func() {
			client.Policies.Delete(context.Background(), ism.PoliciesDeleteReq{Policy: testRetentionPolicy})
		})
		transitions := []ism.PolicyStateTransition{
			{
				StateName: "delete",
				Conditions: &ism.PolicyStateTransitionCondition{
					MinIndexAge: "1h",
				},
			},
		}
		_, err = client.Policies.Put(
			context.Background(),
			ism.PoliciesPutReq{
				Policy: testRetentionPolicy,
				Body: ism.PoliciesPutBody{
					Policy: ism.PolicyBody{
						Description:  "test policy with transitions conditions",
						DefaultState: "test-transitions",
						States: []ism.PolicyState{
							{
								Name:        "test-transitions",
								Transitions: &transitions,
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
								IndexPatterns: []string{"test-transitions"},
								Priority:      21,
							},
						},
					},
				},
			},
		)
		require.Nil(t, err)
	})
}

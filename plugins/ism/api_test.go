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

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
	"github.com/opensearch-project/opensearch-go/v4/plugins/ism"
	osismtest "github.com/opensearch-project/opensearch-go/v4/plugins/ism/internal/test"
)

func TestClient(t *testing.T) {
	t.Parallel()
	client, err := osismtest.NewClient(t)
	require.NoError(t, err)

	osClient, err := testutil.NewClient(t)
	require.NoError(t, err)

	failingClient, err := osismtest.CreateFailingClient()
	require.NoError(t, err)

	testPolicy := testutil.MustUniqueString(t, "test-policy")
	testIndex := []string{testutil.MustUniqueString(t, "test-policy-index")}

	t.Cleanup(func() { client.Policies.Delete(t.Context(), ism.PoliciesDeleteReq{Policy: testPolicy}) })
	_, err = client.Policies.Put(
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
					DefaultState: "test",
					States: []ism.PolicyState{
						{
							Name: "test",
							Actions: []ism.PolicyStateAction{
								{
									Delete: &ism.PolicyStateDelete{},
								},
							},
						},
					},
					Template: []ism.Template{
						{
							IndexPatterns: []string{testIndex[0] + "*"},
							Priority:      22,
						},
					},
				},
			},
		},
	)
	require.NoError(t, err)

	t.Cleanup(func() { osClient.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{Indices: testIndex}) })
	_, err = osClient.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: testIndex[0]})
	require.NoError(t, err)

	type clientTests struct {
		Name    string
		Results func() (osismtest.Response, error)
	}

	// Separate test for Add with proper setup for each subtest
	t.Run("Add", func(t *testing.T) {
		t.Run("okay", func(t *testing.T) {
			t.Parallel()
			addIndex := []string{testutil.MustUniqueString(t, "test-add-okay")}
			t.Cleanup(func() { osClient.Indices.Delete(t.Context(), opensearchapi.IndicesDeleteReq{Indices: addIndex}) })
			_, err := osClient.Indices.Create(t.Context(), opensearchapi.IndicesCreateReq{Index: addIndex[0]})
			require.NoError(t, err)

			res, err := client.Add(t.Context(), ism.AddReq{Indices: addIndex, Body: ism.AddBody{PolicyID: testPolicy}})
			require.NoError(t, err)
			require.NotNil(t, res)
			require.NotNil(t, res.Inspect().Response)
			testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
		})

		t.Run("inspect", func(t *testing.T) {
			t.Parallel()
			res, err := failingClient.Add(t.Context(), ism.AddReq{})
			require.Error(t, err)
			require.NotNil(t, res)
			osismtest.VerifyInspect(t, res.Inspect())
		})
	})

	testCases := []struct {
		Name  string
		Tests []clientTests
	}{
		{
			Name: "Explain",
			Tests: []clientTests{
				{
					Name: "without body",
					Results: func() (osismtest.Response, error) {
						return client.Explain(t.Context(), &ism.ExplainReq{Indices: testIndex})
					},
				},
				{
					Name: "inspect",
					Results: func() (osismtest.Response, error) {
						return failingClient.Explain(t.Context(), &ism.ExplainReq{})
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
						return client.Change(t.Context(), ism.ChangeReq{Indices: testIndex, Body: ism.ChangeBody{PolicyID: testPolicy, State: "delete"}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osismtest.Response, error) {
						return failingClient.Change(t.Context(), ism.ChangeReq{})
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
						return client.Retry(t.Context(), ism.RetryReq{Indices: testIndex})
					},
				},
				{
					Name: "with body",
					Results: func() (osismtest.Response, error) {
						return client.Retry(t.Context(), ism.RetryReq{Indices: testIndex, Body: &ism.RetryBody{State: "test"}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osismtest.Response, error) {
						return failingClient.Retry(t.Context(), ism.RetryReq{})
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
						return client.Remove(t.Context(), ism.RemoveReq{Indices: testIndex})
					},
				},
				{
					Name: "inspect",
					Results: func() (osismtest.Response, error) {
						return failingClient.Remove(t.Context(), ism.RemoveReq{})
					},
				},
			},
		},
		{
			Name: "RefreshSearchAnalyzers",
			Tests: []clientTests{
				{
					Name: "with request",
					Results: func() (osismtest.Response, error) {
						return client.RefreshSearchAnalyzers(t.Context(), ism.RefreshSearchAnalyzersReq{Indices: testIndex})
					},
				},
				{
					Name: "inspect",
					Results: func() (osismtest.Response, error) {
						return failingClient.RefreshSearchAnalyzers(t.Context(), ism.RefreshSearchAnalyzersReq{Indices: []string{"*"}})
					},
				},
			},
		},
	}
	for _, value := range testCases {
		t.Run(value.Name, func(t *testing.T) {
			t.Parallel()
			for _, testCase := range value.Tests {
				t.Run(testCase.Name, func(t *testing.T) {
					t.Parallel()
					res, err := testCase.Results()
					if testCase.Name == "inspect" {
						require.Error(t, err)
						assert.NotNil(t, res)
						osismtest.VerifyInspect(t, res.Inspect())
					} else {
						require.NoError(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
						if value.Name != "Explain" {
							testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
						}
					}
				})
			}
		})
	}
	t.Run("ValidateResponse", func(t *testing.T) {
		t.Parallel()
		t.Run("Explain", func(t *testing.T) {
			resp, err := client.Explain(t.Context(), &ism.ExplainReq{Indices: testIndex})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, &resp, resp.Inspect().Response)
		})
		t.Run("Explain with validate_action", func(t *testing.T) {
			testutil.SkipIfBelowVersion(t, osClient, 2, 4, "Explain with validate_action")
			resp, err := client.Explain(
				t.Context(),
				&ism.ExplainReq{
					Indices: testIndex,
					Params:  ism.ExplainParams{ShowPolicy: true, ValidateAction: true},
				},
			)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, &resp, resp.Inspect().Response)
		})
		t.Run("Explain with show_policy", func(t *testing.T) {
			testutil.SkipIfBelowVersion(t, osClient, 1, 3, "Explain with show_policy")
			resp, err := client.Explain(t.Context(), &ism.ExplainReq{Indices: testIndex, Params: ism.ExplainParams{ShowPolicy: true}})
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, &resp, resp.Inspect().Response)
		})
	})

	t.Run("Put Policy with Transitions Conditions", func(t *testing.T) {
		t.Parallel()
		testRetentionPolicy := testutil.MustUniqueString(t, "test-retention-policy")
		testTransitionsPattern := testutil.MustUniqueString(t, "test-transitions")
		t.Cleanup(func() {
			client.Policies.Delete(t.Context(), ism.PoliciesDeleteReq{Policy: testRetentionPolicy})
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
			t.Context(),
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
								IndexPatterns: []string{testTransitionsPattern},
								Priority:      21,
							},
						},
					},
				},
			},
		)
		require.NoError(t, err)
	})
}

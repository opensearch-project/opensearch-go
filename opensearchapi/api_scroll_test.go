// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
)

func TestScrollClient(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.NoError(t, err)

	search, err := client.Search(
		t.Context(),
		&opensearchapi.SearchReq{
			Indices: []string{"*"},
			Params:  opensearchapi.SearchParams{Scroll: 5 * time.Minute},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, search.ScrollID, "ScrollID is nil")

	type scrollTests struct {
		Name    string
		Results func() (osapitest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []scrollTests
	}{
		{
			Name: "Get",
			Tests: []scrollTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Scroll.Get(t.Context(), opensearchapi.ScrollGetReq{ScrollID: *search.ScrollID})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Scroll.Get(t.Context(), opensearchapi.ScrollGetReq{})
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []scrollTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Scroll.Delete(t.Context(), opensearchapi.ScrollDeleteReq{ScrollIDs: []string{"_all"}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Scroll.Delete(t.Context(), opensearchapi.ScrollDeleteReq{})
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
						osapitest.VerifyInspect(t, res.Inspect())
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

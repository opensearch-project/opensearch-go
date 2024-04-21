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

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/opensearchapi/internal/test"
)

func TestScrollClient(t *testing.T) {
	client, err := ostest.NewClient()
	require.NoError(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.NoError(t, err)

	search, err := client.Search(
		nil,
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
						return client.Scroll.Get(nil, opensearchapi.ScrollGetReq{ScrollID: *search.ScrollID})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Scroll.Get(nil, opensearchapi.ScrollGetReq{})
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
						return client.Scroll.Delete(nil, opensearchapi.ScrollDeleteReq{ScrollIDs: []string{"_all"}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Scroll.Delete(nil, opensearchapi.ScrollDeleteReq{})
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
						assert.Error(t, err)
						assert.NotNil(t, res)
						osapitest.VerifyInspect(t, res.Inspect())
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

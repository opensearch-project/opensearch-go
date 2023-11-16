// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration

package opensearchapi_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v2/opensearchapi/internal/test"
)

func TestScrollClient(t *testing.T) {
	client, err := opensearchapi.NewDefaultClient()
	require.Nil(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	search, err := client.Search(
		nil,
		&opensearchapi.SearchReq{
			Indices: []string{"test*"},
			Params:  opensearchapi.SearchParams{Scroll: 5 * time.Minute},
		},
	)
	require.Nil(t, err)
	require.NotNil(t, search.ScrollID)

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
						assert.NotNil(t, err)
						assert.NotNil(t, res)
						osapitest.VerifyInspect(t, res.Inspect())
					} else {
						require.Nil(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
						osapitest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
					}
				})
			}
		})
	}
}

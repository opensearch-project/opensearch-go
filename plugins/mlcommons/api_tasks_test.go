// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (plugins || plugin_ml_commons)

package mlcommons_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/plugins/mlcommons"
	osmlcommonstest "github.com/opensearch-project/opensearch-go/v4/plugins/mlcommons/internal/test"
)

// TestTasksClient exercises every Tasks.* endpoint against the failing httptest server.
func TestTasksClient(t *testing.T) {
	t.Parallel()
	failingClient, err := osmlcommonstest.CreateFailingClient(t)
	require.NoError(t, err)

	type tasksTest struct {
		Name    string
		Results func() (osmlcommonstest.Response, error)
	}

	tests := []tasksTest{
		{
			Name: "Get",
			Results: func() (osmlcommonstest.Response, error) {
				return failingClient.Tasks.Get(t.Context(), mlcommons.TasksGetReq{TaskID: "missing"})
			},
		},
		{
			Name: "Delete",
			Results: func() (osmlcommonstest.Response, error) {
				return failingClient.Tasks.Delete(t.Context(), mlcommons.TasksDeleteReq{TaskID: "missing"})
			},
		},
		{
			Name: "Search",
			Results: func() (osmlcommonstest.Response, error) {
				return failingClient.Tasks.Search(t.Context(), &mlcommons.TasksSearchReq{
					Body: json.RawMessage(`{"query":{"match_all":{}}}`),
				})
			},
		},
		{
			Name: "SearchNil",
			Results: func() (osmlcommonstest.Response, error) {
				return failingClient.Tasks.Search(t.Context(), nil)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			res, err := tc.Results()
			require.Error(t, err)
			require.NotNil(t, res)
			osmlcommonstest.VerifyInspect(t, res.Inspect())
		})
	}
}

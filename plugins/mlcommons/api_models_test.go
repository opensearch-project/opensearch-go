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

// TestModelsClient exercises every Models.* endpoint against the failing httptest server.
// This validates request construction, error parsing, and Inspect() across all operations
// without depending on a live ML Commons-enabled cluster (model download + deploy is too
// heavy for CI). Live-cluster smoke testing is provided by _samples/mlcommons.go.
func TestModelsClient(t *testing.T) {
	t.Parallel()
	failingClient, err := osmlcommonstest.CreateFailingClient(t)
	require.NoError(t, err)

	type modelsTest struct {
		Name    string
		Results func() (osmlcommonstest.Response, error)
	}

	tests := []modelsTest{
		{
			Name: "Register",
			Results: func() (osmlcommonstest.Response, error) {
				return failingClient.Models.Register(t.Context(), mlcommons.ModelsRegisterReq{
					Body: mlcommons.ModelsRegisterBody{Name: "test"},
				})
			},
		},
		{
			Name: "Get",
			Results: func() (osmlcommonstest.Response, error) {
				return failingClient.Models.Get(t.Context(), mlcommons.ModelsGetReq{ModelID: "missing"})
			},
		},
		{
			Name: "Update",
			Results: func() (osmlcommonstest.Response, error) {
				return failingClient.Models.Update(t.Context(), mlcommons.ModelsUpdateReq{
					ModelID: "missing",
					Body:    mlcommons.ModelsUpdateBody{Description: "test"},
				})
			},
		},
		{
			Name: "Deploy",
			Results: func() (osmlcommonstest.Response, error) {
				return failingClient.Models.Deploy(t.Context(), mlcommons.ModelsDeployReq{ModelID: "missing"})
			},
		},
		{
			Name: "DeployWithBody",
			Results: func() (osmlcommonstest.Response, error) {
				return failingClient.Models.Deploy(t.Context(), mlcommons.ModelsDeployReq{
					ModelID: "missing",
					Body:    &mlcommons.ModelsDeployBody{NodeIDs: []string{"node-1"}},
				})
			},
		},
		{
			Name: "UndeploySingle",
			Results: func() (osmlcommonstest.Response, error) {
				return failingClient.Models.Undeploy(t.Context(), mlcommons.ModelsUndeployReq{ModelID: "missing"})
			},
		},
		{
			Name: "UndeployBatch",
			Results: func() (osmlcommonstest.Response, error) {
				return failingClient.Models.Undeploy(t.Context(), mlcommons.ModelsUndeployReq{
					Body: &mlcommons.ModelsUndeployBody{ModelIDs: []string{"a", "b"}},
				})
			},
		},
		{
			Name: "Delete",
			Results: func() (osmlcommonstest.Response, error) {
				return failingClient.Models.Delete(t.Context(), mlcommons.ModelsDeleteReq{ModelID: "missing"})
			},
		},
		{
			Name: "Search",
			Results: func() (osmlcommonstest.Response, error) {
				return failingClient.Models.Search(t.Context(), &mlcommons.ModelsSearchReq{
					Body: json.RawMessage(`{"query":{"match_all":{}}}`),
				})
			},
		},
		{
			Name: "SearchNil",
			Results: func() (osmlcommonstest.Response, error) {
				return failingClient.Models.Search(t.Context(), nil)
			},
		},
		{
			Name: "Predict",
			Results: func() (osmlcommonstest.Response, error) {
				return failingClient.Models.Predict(t.Context(), mlcommons.ModelsPredictReq{
					ModelID: "missing",
					Body:    json.RawMessage(`{"text_docs":["hello"]}`),
				})
			},
		},
		{
			Name: "UploadChunk",
			Results: func() (osmlcommonstest.Response, error) {
				return failingClient.Models.UploadChunk(t.Context(), mlcommons.ModelsUploadChunkReq{
					ModelID:     "missing",
					ChunkNumber: 0,
					Body:        []byte{0x01, 0x02, 0x03},
				})
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

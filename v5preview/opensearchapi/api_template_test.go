// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration

package opensearchapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi/internal/osapitest"
	"github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi/testutil"
)

func TestManual_IndexTemplate(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	tests := []struct {
		name     string
		template string
		body     string
	}{
		{
			name:     "basic template",
			template: testutil.MustUniqueString(t, "tmpl-basic"),
			body:     `{"index_patterns":["tmpl-basic-*"],"template":{"settings":{"number_of_replicas":"0"}}}`,
		},
		{
			name:     "template with mapping",
			template: testutil.MustUniqueString(t, "tmpl-mapping"),
			body:     `{"index_patterns":["tmpl-mapping-*"],"template":{"mappings":{"properties":{"status":{"type":"keyword"}}}}}`,
		},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			_, _ = client.Indices.DeleteIndexTemplate(context.Background(), opensearchapi.IndicesDeleteIndexTemplateReq{Name: tt.template})
		})
	}

	for _, tt := range tests {
		t.Run("create/"+tt.name, func(t *testing.T) {
			resp, err := client.Indices.PutIndexTemplate(t.Context(), opensearchapi.IndicesPutIndexTemplateReq{
				Name:       tt.template,
				BodyReader: strings.NewReader(tt.body),
			})
			require.NoError(t, err)
			require.True(t, resp.Acknowledged)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	}

	for _, tt := range tests {
		t.Run("get/"+tt.name, func(t *testing.T) {
			resp, err := client.Indices.GetIndexTemplate(t.Context(), opensearchapi.IndicesGetIndexTemplateReq{
				Name: tt.template,
			})
			require.NoError(t, err)
			require.NotEmpty(t, resp.IndexTemplates)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	}

	for _, tt := range tests {
		t.Run("exists/"+tt.name, func(t *testing.T) {
			resp, err := client.Indices.ExistsIndexTemplate(t.Context(), opensearchapi.IndicesExistsIndexTemplateReq{
				Name: tt.template,
			})
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}

	for _, tt := range tests {
		t.Run("delete/"+tt.name, func(t *testing.T) {
			resp, err := client.Indices.DeleteIndexTemplate(t.Context(), opensearchapi.IndicesDeleteIndexTemplateReq{
				Name: tt.template,
			})
			require.NoError(t, err)
			require.True(t, resp.Acknowledged)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	}

	t.Run("inspect", func(t *testing.T) {
		failingClient, err := osapitest.CreateFailingClient(t)
		require.NoError(t, err)

		res, err := failingClient.Indices.GetIndexTemplate(t.Context(), opensearchapi.IndicesGetIndexTemplateReq{
			Name: "nonexistent",
		})
		require.Error(t, err)
		require.NotNil(t, res)
		osapitest.VerifyInspect(t, res.Inspect())
	})
}

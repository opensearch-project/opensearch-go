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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
)

// TestManual_PITSegments covers the cat.pit_segments and cat.all_pit_segments
// success paths, which the generated TestCatPITSegments / TestCatAllPITSegments
// skip because they need an active point-in-time context.
func TestManual_PITSegments(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)

	testutil.SkipIfVersion(t, client, "<", "2.4", "PITSegments")

	index := testutil.MustUniqueString(t, "test-pit-segments")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Index: []string{index}})
	})

	_, err = client.Index(t.Context(), opensearchapi.IndexReq{
		Index:  index,
		ID:     "1",
		Body:   strings.NewReader(`{"title":"PIT segments test"}`),
		Params: &opensearchapi.IndexParams{Refresh: "true"},
	})
	require.NoError(t, err)

	createResp, err := client.CreatePIT(t.Context(), &opensearchapi.CreatePITReq{
		Index:  []string{index},
		Params: &opensearchapi.CreatePITParams{KeepAlive: 1 * time.Minute},
	})
	require.NoError(t, err)
	require.NotNil(t, createResp.PITID)
	pitID := *createResp.PITID
	require.NotEmpty(t, pitID)

	t.Cleanup(func() {
		_, _ = client.DeletePIT(context.Background(), &opensearchapi.DeletePITReq{
			Body: &opensearchapi.DeletePITBody{PITID: []string{pitID}},
		})
	})

	cases := []struct {
		name string
		exec func(ctx context.Context) (interface{ Inspect() opensearchapi.Inspect }, error)
	}{
		{
			name: "cat.pit_segments by PIT ID",
			exec: func(ctx context.Context) (interface{ Inspect() opensearchapi.Inspect }, error) {
				return client.Cat.PITSegments(ctx, &opensearchapi.CatPITSegmentsReq{
					Body: &opensearchapi.CatPITSegmentsBody{PITID: []string{pitID}},
					Params: &opensearchapi.CatPITSegmentsParams{
						DebugParams: opensearchapi.DebugParams{Format: "json"},
					},
				})
			},
		},
		{
			name: "cat.all_pit_segments",
			exec: func(ctx context.Context) (interface{ Inspect() opensearchapi.Inspect }, error) {
				return client.Cat.AllPITSegments(ctx, &opensearchapi.CatAllPITSegmentsReq{
					Params: &opensearchapi.CatAllPITSegmentsParams{
						DebugParams: opensearchapi.DebugParams{Format: "json"},
					},
				})
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := c.exec(t.Context())
			require.NoError(t, err)
			require.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp, resp.Inspect().Response)
		})
	}
}

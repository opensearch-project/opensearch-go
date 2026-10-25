// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

// Package pitexpiry holds the one PIT integration test that has to outlast the
// server's expiry sweep. It lives in its own package so go test runs it
// alongside the other packages instead of ahead of the opensearchapi tests,
// where its wait would sit on the critical path.
package pitexpiry_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
)

// keepAliveWindow outlasts the longest an unsearched PIT survives: its 20s
// keep_alive plus one sweep, which search.keep_alive_interval sets to a minute
// by default.
const keepAliveWindow = 90 * time.Second

// TestIntegration_PITKeepAliveRefresh checks that PIT.Search keeps a PIT open
// without sending keep_alive: the server restarts the keep_alive set on create
// with every search, so searching every 5s keeps a PIT with a 20s keep_alive
// open across the expiry sweep. An unsearched PIT would be gone within about
// 80s and the window spans that.
func TestIntegration_PITKeepAliveRefresh(t *testing.T) {
	if testing.Short() {
		t.Skip("spans the server's expiry sweep")
	}
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	testutil.SkipIfVersion(t, client, "<", "2.4", "PIT")

	index := testutil.MustUniqueString(t, "test-pit-expiry")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})
	for i := 1; i <= 7; i++ {
		_, err := client.Doc.Index(t.Context(), opensearchapi.IndexReq{
			Index: index,
			ID:    fmt.Sprint(i),
			Body:  strings.NewReader(fmt.Sprintf(`{"n":%d}`, i)),
		})
		require.NoError(t, err)
	}
	_, err = client.Indices.Refresh(t.Context(), &opensearchapi.IndicesRefreshReq{Indices: []string{index}})
	require.NoError(t, err)

	pit, err := client.PIT.Open(t.Context(), &opensearchapi.CreatePITReq{
		Indices: []string{index},
		Params:  &opensearchapi.CreatePITParams{KeepAlive: 20 * time.Second},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pit.Close(context.Background()) }) // t.Context() is done by cleanup

	ctx := t.Context()
	gone := func() bool {
		if _, err := pit.Search(ctx, &opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{Size: new(1)}}); err != nil {
			return true
		}
		resp, err := client.PIT.GetAll(ctx, nil)
		if err != nil {
			return true
		}
		for _, p := range resp.PITs {
			if p.PITID == pit.ID() {
				return false
			}
		}
		return true
	}
	require.Never(t, gone, keepAliveWindow, 5*time.Second)
}

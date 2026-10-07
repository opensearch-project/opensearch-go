// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchapi)

package opensearchapi_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi/testutil"
)

// pitDocs is how many documents most PIT integration tests index; with a page
// size of 3 it spans a full page, a full page and a short page.
const pitDocs = 7

// newPITIndex creates an index holding docs documents with n = 1..docs.
func newPITIndex(t *testing.T, client *opensearchapi.Client, docs int) string {
	t.Helper()
	index := testutil.MustUniqueString(t, "test-pit-handle")
	t.Cleanup(func() {
		_, _ = client.Indices.Delete(context.Background(), &opensearchapi.IndicesDeleteReq{Indices: []string{index}})
	})
	for i := 1; i <= docs; i++ {
		_, err := client.Doc.Index(t.Context(), opensearchapi.IndexReq{
			Index: index,
			ID:    fmt.Sprint(i),
			Body:  strings.NewReader(fmt.Sprintf(`{"n":%d}`, i)),
		})
		require.NoError(t, err)
	}
	_, err := client.Indices.Refresh(t.Context(), &opensearchapi.IndicesRefreshReq{Indices: []string{index}})
	require.NoError(t, err)
	return index
}

// byN sorts on the unique field n, so it is its own tiebreaker.
func byN() *opensearchapi.Sort {
	sort := opensearchapi.NewSortFromArray([]opensearchapi.SortCombinations{
		opensearchapi.NewSortCombinationsFromFieldSortMap(map[string]opensearchapi.FieldSort{"n": {Order: new("asc")}}),
	})
	return &sort
}

// pitExists reports whether id is among the cluster's open PITs.
func pitExists(t *testing.T, client *opensearchapi.Client, id opensearchapi.PITID) bool {
	t.Helper()
	ok, err := findPIT(t.Context(), client, id)
	require.NoError(t, err)
	return ok
}

// findPIT reports whether id is among the cluster's open PITs. Unlike pitExists
// it does not call require, so it is safe in a require.Eventually or Never
// condition, which testify runs on another goroutine.
func findPIT(ctx context.Context, client *opensearchapi.Client, id opensearchapi.PITID) (bool, error) {
	resp, err := client.PIT.GetAll(ctx, nil)
	if err != nil {
		return false, err
	}
	for _, p := range resp.PITs {
		if p.PITID == id {
			return true, nil
		}
	}
	return false, nil
}

// TestIntegration_PITHandle is the PIT lifecycle end to end: create a PIT,
// page through 6 documents 2 at a time, and delete the PIT.
func TestIntegration_PITHandle(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	testutil.SkipIfVersion(t, client, "<", "2.4", "PIT")
	index := newPITIndex(t, client, 6)

	// Open relies on the server accepting its default keep_alive, sent as 300000ms.
	pit, err := client.PIT.Open(t.Context(), &opensearchapi.CreatePITReq{Indices: []string{index}})
	require.NoError(t, err)
	require.True(t, pitExists(t, client, pit.ID()))

	pages, errf := pit.SearchAfterPages(t.Context(), &opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{Sort: byN(), Size: new(2)}})
	var got [][]string
	for resp := range pages {
		var ids []string
		for _, h := range resp.Hits.Hits {
			ids = append(ids, *h.ID)
		}
		got = append(got, ids)
	}
	require.NoError(t, errf())
	require.Equal(t, [][]string{{"1", "2"}, {"3", "4"}, {"5", "6"}}, got)
	require.True(t, pitExists(t, client, pit.ID()), "the handle's iterators must leave the PIT open")

	require.NoError(t, pit.Close(t.Context()))
	require.False(t, pitExists(t, client, pit.ID()), "the PIT must be deleted")
}

func TestIntegration_PITOneShotSearchAfter(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	testutil.SkipIfVersion(t, client, "<", "2.4", "PIT")
	index := newPITIndex(t, client, pitDocs)

	pages, errf := client.PIT.SearchAfterPages(t.Context(),
		&opensearchapi.CreatePITReq{Indices: []string{index}},
		&opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{Sort: byN(), Size: new(3)}})
	var sizes []int
	var pitID opensearchapi.PITID
	for resp := range pages {
		sizes = append(sizes, len(resp.Hits.Hits))
		if cur, ok := resp.Cursor(); ok {
			pitID = cur.PIT
		}
	}
	require.NoError(t, errf())
	require.Equal(t, []int{3, 3, 1}, sizes)
	require.NotEmpty(t, pitID)
	require.False(t, pitExists(t, client, pitID), "the one-shot must delete its PIT")
}

// TestIntegration_SearchAfterResume pages a PIT across two requests: the first
// reads page one and keeps its SearchCursor, and the second applies the cursor
// and resumes through client.SearchAfter, with no handle. It also checks that
// the server echoes the PIT ID the cursor carries.
func TestIntegration_SearchAfterResume(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	testutil.SkipIfVersion(t, client, "<", "2.4", "PIT")
	index := newPITIndex(t, client, pitDocs)

	pit, err := client.PIT.Open(t.Context(), &opensearchapi.CreatePITReq{Indices: []string{index}})
	require.NoError(t, err)
	resp, err := pit.Search(t.Context(), &opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{Sort: byN(), Size: new(3)}})
	require.NoError(t, err)
	require.Len(t, resp.Hits.Hits, 3)
	cur, ok := resp.Cursor()
	require.True(t, ok)
	require.Equal(t, pit.ID(), cur.PIT, "the server echoes the PIT ID")

	resume := cur.Apply(&opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{Sort: byN(), Size: new(3)}})
	hits, errf := client.SearchAfter(t.Context(), resume)
	var ids []string
	for h := range hits {
		ids = append(ids, *h.ID)
	}
	require.NoError(t, errf())
	require.Equal(t, []string{"4", "5", "6", "7"}, ids)
	require.True(t, pitExists(t, client, cur.PIT), "the client iterators must leave the PIT open")

	require.NoError(t, pit.Close(t.Context()))
	pages, errf := client.SearchAfterPages(t.Context(), resume)
	for range pages {
		require.Fail(t, "a deleted PIT must yield no page")
	}
	_, missing := errors.AsType[*opensearchapi.SearchContextMissingError](errf())
	require.True(t, missing, "want SearchContextMissingError, got %v", errf())
}

// TestIntegration_SearchContextMissing checks which real server errors the
// generated search and scroll dispatch report as SearchContextMissingError.
func TestIntegration_SearchContextMissing(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	index := newPITIndex(t, client, pitDocs)

	scroll, err := client.Search(t.Context(), &opensearchapi.SearchReq{
		Indices: []string{index},
		Body:    &opensearchapi.SearchBody{Size: new(1)},
		Params:  &opensearchapi.SearchParams{Scroll: time.Minute},
	})
	require.NoError(t, err)
	require.NotNil(t, scroll.ScrollID)
	_, err = client.Scroll.Delete(t.Context(), &opensearchapi.ClearScrollReq{ScrollID: []string{*scroll.ScrollID}})
	require.NoError(t, err)

	tests := []struct {
		name        string
		run         func() error
		wantMissing bool
	}{
		{
			name: "a cleared scroll",
			run: func() error {
				_, err := client.Scroll.Get(t.Context(), opensearchapi.ScrollReq{ScrollID: *scroll.ScrollID})
				return err
			},
			wantMissing: true,
		},
		{
			name: "a malformed PIT ID",
			run: func() error {
				_, err := client.Search(t.Context(), &opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{
					PIT: &opensearchapi.SearchPointInTimeReference{ID: pitID("not-a-pit-id")},
				}})
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			require.Error(t, err)
			_, missing := errors.AsType[*opensearchapi.SearchContextMissingError](err)
			require.Equal(t, tt.wantMissing, missing, "%v", err)
		})
	}
}

// TestIntegration_SearchAfterNoPIT pages live indices without a PIT.
func TestIntegration_SearchAfterNoPIT(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	index := newPITIndex(t, client, pitDocs)

	hits, errf := client.SearchAfter(t.Context(), &opensearchapi.SearchReq{
		Indices: []string{index},
		Body:    &opensearchapi.SearchBody{Sort: byN(), Size: new(3)},
	})
	var ids []string
	for h := range hits {
		ids = append(ids, *h.ID)
	}
	require.NoError(t, errf())
	require.Equal(t, []string{"1", "2", "3", "4", "5", "6", "7"}, ids)
}

// TestIntegration_PITDeleteMissing records what the server returns when a PIT
// that is already gone is deleted, and checks Close treats that as closed.
func TestIntegration_PITDeleteMissing(t *testing.T) {
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	testutil.SkipIfVersion(t, client, "<", "2.4", "PIT")
	index := newPITIndex(t, client, pitDocs)

	pit, err := client.PIT.Open(t.Context(), &opensearchapi.CreatePITReq{Indices: []string{index}})
	require.NoError(t, err)
	deleteReq := &opensearchapi.DeletePITReq{Body: &opensearchapi.DeletePITBody{PITID: []opensearchapi.PITID{pit.ID()}}}
	_, err = client.PIT.Delete(t.Context(), deleteReq)
	require.NoError(t, err)

	resp, err := client.PIT.Delete(t.Context(), deleteReq)
	status := 0
	if r := resp.Inspect().Response; r != nil {
		status = r.StatusCode
	}
	t.Logf("deleting a missing PIT: status=%d err=%q body=%q", status, fmt.Sprint(err), fmt.Sprint(resp.Inspect().Response))
	require.Contains(t, []int{http.StatusOK, http.StatusNotFound}, status)

	require.NoError(t, pit.Close(t.Context()), "a PIT the server no longer has counts as closed")
	_, err = pit.Search(t.Context(), &opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{}})
	require.ErrorIs(t, err, opensearchapi.ErrPITClosed)
}

// TestIntegration_PITKeepAliveRefresh checks that PIT.Search keeps a PIT open
// without sending keep_alive: the server restarts the keep_alive set on create
// with every search, so searching every 5s keeps a PIT with a 20s keep_alive
// open for 2 minutes. Expired PITs are freed by a periodic sweep (about a
// minute by default), so an unsearched PIT would be gone within about 80s and
// the window spans that.
func TestIntegration_PITKeepAliveRefresh(t *testing.T) {
	if testing.Short() {
		t.Skip("spans the server's expiry sweep")
	}
	client, err := testutil.NewClient(t)
	require.NoError(t, err)
	testutil.SkipIfVersion(t, client, "<", "2.4", "PIT")
	index := newPITIndex(t, client, pitDocs)

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
		ok, err := findPIT(ctx, client, pit.ID())
		return err != nil || !ok
	}
	require.Never(t, gone, 2*time.Minute, 5*time.Second)
}

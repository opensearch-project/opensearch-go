// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	opensearch "github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/errmask"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// pitRequest is one request the fake PIT cluster received.
type pitRequest struct {
	method string
	path   string
	query  string
	body   string
}

// fakePITConfig sets how the fake PIT cluster answers. It holds no lock, so
// test tables can copy it.
type fakePITConfig struct {
	createStatus int  // 0 means 200
	createNoID   bool // create succeeds but its response has no pit_id
	failedShards int  // failed shards reported on every search

	// searchErrStatus, if set, answers every search with this status and an
	// all-shards-failed error whose root cause type is searchErrType.
	searchErrStatus int
	searchErrType   string

	// deleteStatuses answers the delete requests in order, one status each; once
	// they run out, deletes return 200.
	deleteStatuses []int
	// unsuccessfulDeletes is how many 200 deletes answer "successful": false before
	// they start succeeding.
	unsuccessfulDeletes int

	// Paging mode, used when pages > 0. The page is read from the request's
	// search_after, so a retried page returns the same hits: pages 1..pages hold
	// hitsPerPage hits each, with sort values page*100+i, and later pages are empty.
	pages, hitsPerPage int

	// Per-page faults, keyed by 1-based page. A times field is how many attempts
	// at that page fail before it succeeds; 0 means every attempt fails.
	failSearchOnPage      int    // answers HTTP 500
	failShardOnPage       int    // reports one failed shard
	failShardTimes        int    //
	failShardReason       string // failed shard's reason type; "" means query_shard_exception
	failShardCausedBy     string // if set, the reason's caused_by type
	timedOutOnPage        int    // reports timed_out: true
	timedOutTimes         int    //
	terminatedEarlyOnPage int    // reports terminated_early: true

	// onSearch, if set, runs for each paging search before it is answered.
	onSearch func(page, attempt int)
}

// searchAfterRe finds the first search_after value in a search body.
var searchAfterRe = regexp.MustCompile(`"search_after":\[(\d+)`)

// pageOf returns the 1-based page a paging search asks for, from its search_after.
func pageOf(body string) int {
	m := searchAfterRe.FindStringSubmatch(body)
	if m == nil {
		return 1
	}
	v, _ := strconv.Atoi(m[1])
	return v/100 + 1
}

// faulty reports whether attempt at a faulty page fails, given its times field.
func faulty(times, attempt int) bool { return times == 0 || attempt <= times }

// fakePITCluster answers create PIT, search and delete PIT, and records every
// request. Its fakePITConfig is read-only; everything it counts lives in mu.
type fakePITCluster struct {
	fakePITConfig

	mu struct {
		sync.Mutex
		reqs      []pitRequest
		attempts  map[int]int // paging searches seen per page
		deletes   int         // delete requests answered, indexing deleteStatuses
		okDeletes int         // 200 deletes answered, counted against unsuccessfulDeletes
	}
}

func (f *fakePITCluster) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.mu.reqs = append(f.mu.reqs, pitRequest{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: string(body)})
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/_search/point_in_time"):
		if f.createStatus != 0 {
			w.WriteHeader(f.createStatus)
			fmt.Fprintf(w, `{"error":{"type":"x","reason":"create failed"},"status":%d}`, f.createStatus)
			return
		}
		if f.createNoID {
			fmt.Fprint(w, `{"_shards":{"total":1,"successful":1,"failed":0},"creation_time":1}`)
			return
		}
		fmt.Fprint(w, `{"pit_id":"pit-1","_shards":{"total":1,"successful":1,"failed":0},"creation_time":1}`)
	case r.Method == http.MethodDelete && (r.URL.Path == "/_search/point_in_time" || r.URL.Path == "/_search/point_in_time/_all"):
		status := http.StatusOK
		f.mu.Lock()
		if f.mu.deletes < len(f.deleteStatuses) {
			status = f.deleteStatuses[f.mu.deletes]
		}
		f.mu.deletes++
		f.mu.Unlock()
		if status != http.StatusOK {
			w.WriteHeader(status)
			fmt.Fprintf(w, `{"error":{"type":"x","reason":"delete failed"},"status":%d}`, status)
			return
		}
		f.mu.Lock()
		successful := f.mu.okDeletes >= f.unsuccessfulDeletes
		f.mu.okDeletes++
		f.mu.Unlock()
		fmt.Fprintf(w, `{"pits":[{"pit_id":"pit-1","successful":%t}]}`, successful)
	case strings.HasSuffix(r.URL.Path, "/_search") && f.searchErrStatus != 0:
		w.WriteHeader(f.searchErrStatus)
		fmt.Fprintf(w, `{"error":{"root_cause":[{"type":%q,"reason":"x"}],"type":"search_phase_execution_exception",`+
			`"reason":"all shards failed"},"status":%d}`, f.searchErrType, f.searchErrStatus)
	case strings.HasSuffix(r.URL.Path, "/_search") && f.pages > 0:
		page := pageOf(string(body))
		f.mu.Lock()
		if f.mu.attempts == nil {
			f.mu.attempts = map[int]int{}
		}
		f.mu.attempts[page]++
		attempt := f.mu.attempts[page]
		f.mu.Unlock()
		if f.onSearch != nil {
			f.onSearch(page, attempt)
		}
		if page == f.failSearchOnPage {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"type":"x","reason":"search failed"},"status":500}`)
			return
		}
		var hits []string
		if page <= f.pages {
			for i := range f.hitsPerPage {
				hits = append(hits, fmt.Sprintf(`{"_id":"%d-%d","sort":[%d]}`, page, i, page*100+i))
			}
		}
		failed, failures := 0, ""
		if page == f.failShardOnPage && faulty(f.failShardTimes, attempt) {
			reason := f.failShardReason
			if reason == "" {
				reason = "query_shard_exception"
			}
			causedBy := ""
			if f.failShardCausedBy != "" {
				causedBy = fmt.Sprintf(`,"caused_by":{"type":%q,"reason":"x"}`, f.failShardCausedBy)
			}
			failed = 1
			failures = fmt.Sprintf(`,"failures":[{"shard":0,"index":"idx","node":"n1","reason":{"type":%q,"reason":"x"%s}}]`, reason, causedBy)
		}
		timedOut := page == f.timedOutOnPage && faulty(f.timedOutTimes, attempt)
		early := ""
		if page == f.terminatedEarlyOnPage {
			early = `"terminated_early":true,`
		}
		fmt.Fprintf(w, `{"pit_id":"pit-1","took":1,"timed_out":%t,%s`+
			`"_shards":{"total":2,"successful":%d,"failed":%d%s},"hits":{"hits":[%s]}}`,
			timedOut, early, 2-failed, failed, failures, strings.Join(hits, ","))
	case strings.HasSuffix(r.URL.Path, "/_search"):
		fmt.Fprintf(w, `{"pit_id":"pit-1","took":1,"timed_out":false,`+
			`"_shards":{"total":2,"successful":%d,"failed":%d},"hits":{"hits":[{"_id":"a","sort":[1]}]}}`,
			2-f.failedShards, f.failedShards)
	default:
		fmt.Fprint(w, `{"version":{"number":"2.19.5","distribution":"opensearch"}}`)
	}
}

// requests returns the recorded requests with this method (any method when
// empty) whose path ends in suffix.
func (f *fakePITCluster) requests(method, suffix string) []pitRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []pitRequest
	for _, r := range f.mu.reqs {
		if (method == "" || r.method == method) && strings.HasSuffix(r.path, suffix) {
			out = append(out, r)
		}
	}
	return out
}

// deletes and creates return the recorded delete and create PIT requests.
func (f *fakePITCluster) deletes() []pitRequest {
	return f.requests(http.MethodDelete, "/_search/point_in_time")
}

func (f *fakePITCluster) creates() []pitRequest {
	return f.requests(http.MethodPost, "/_search/point_in_time")
}

// pitID returns s as a PIT ID, for test tables. It panics on an empty s.
func pitID(s string) opensearchapi.PITID {
	id, err := opensearchapi.ParsePITID(s)
	if err != nil {
		panic(err)
	}
	return id
}

// newPITClient starts f behind an httptest server and returns a client pointed at it.
func newPITClient(t *testing.T, f *fakePITCluster) *opensearchapi.Client {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(ts.Close)
	client, err := opensearchapi.NewClient(opensearchapi.Config{Client: opensearch.Config{Addresses: []string{ts.URL}}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestPITOpen(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		params        *opensearchapi.CreatePITParams
		createStatus  int
		createNoID    bool
		wantKeepAlive string // keep_alive query value sent on create
		wantErr       bool
	}{
		{name: "no params uses the 300s default", params: nil, wantKeepAlive: "keep_alive=300000ms"},
		{name: "zero keep_alive uses the 300s default", params: &opensearchapi.CreatePITParams{}, wantKeepAlive: "keep_alive=300000ms"},
		{
			name: "caller keep_alive wins", params: &opensearchapi.CreatePITParams{KeepAlive: 90 * time.Second},
			wantKeepAlive: "keep_alive=90000ms",
		},
		{
			name: "create error is returned", params: nil, createStatus: http.StatusBadRequest,
			wantKeepAlive: "keep_alive=300000ms", wantErr: true,
		},
		{
			name: "a create response without a PIT ID is an error", createNoID: true,
			wantKeepAlive: "keep_alive=300000ms", wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakePITCluster{fakePITConfig: fakePITConfig{createStatus: tt.createStatus, createNoID: tt.createNoID}}
			client := newPITClient(t, f)

			pit, err := client.PIT.Open(t.Context(), &opensearchapi.CreatePITReq{Indices: []string{"idx"}, Params: tt.params})

			creates := f.creates()
			require.Len(t, creates, 1)
			require.Equal(t, "/idx/_search/point_in_time", creates[0].path)
			require.Contains(t, creates[0].query, tt.wantKeepAlive)
			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, pit)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "pit-1", pit.ID().String())
		})
	}
}

func TestPITSearch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		req          func() *opensearchapi.SearchReq
		failedShards int
		wantPIT      string // pit object expected in the sent body
		wantErr      string
		wantPartial  bool
	}{
		{
			// No keep_alive: the server keeps the one set on create and restarts its timer on every search.
			name:    "sends the PIT ID only",
			req:     func() *opensearchapi.SearchReq { return &opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{}} },
			wantPIT: `"pit":{"id":"pit-1"}`,
		},
		{
			name: "rejects indices",
			req: func() *opensearchapi.SearchReq {
				return &opensearchapi.SearchReq{Indices: []string{"idx"}, Body: &opensearchapi.SearchBody{}}
			},
			wantErr: "Indices",
		},
		{
			name: "rejects a raw body next to a typed one",
			req: func() *opensearchapi.SearchReq {
				return &opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{}, BodyReader: strings.NewReader(`{}`)}
			},
			wantErr: "BodyReader",
		},
		{
			name:    "rejects a raw body",
			req:     func() *opensearchapi.SearchReq { return &opensearchapi.SearchReq{BodyReader: strings.NewReader(`{}`)} },
			wantErr: "Body",
		},
		{
			name:    "rejects a missing body",
			req:     func() *opensearchapi.SearchReq { return &opensearchapi.SearchReq{} },
			wantErr: "Body",
		},
		{
			name:         "a failed shard is a partial-failure error under the default mask",
			req:          func() *opensearchapi.SearchReq { return &opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{}} },
			failedShards: 1,
			wantPIT:      `"pit":{"id":"pit-1"}`,
			wantPartial:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakePITCluster{fakePITConfig: fakePITConfig{failedShards: tt.failedShards}}
			client := newPITClient(t, f)
			pit, err := client.PIT.Open(t.Context(), &opensearchapi.CreatePITReq{Indices: []string{"idx"}})
			require.NoError(t, err)

			req := tt.req()
			resp, err := pit.Search(t.Context(), req)

			searches := f.requests("", "/_search")
			switch {
			case tt.wantErr != "":
				require.ErrorContains(t, err, tt.wantErr)
				require.Empty(t, searches, "an invalid request must not be sent")
				return
			case tt.wantPartial:
				var pe *opensearchapi.PartialSearchError
				require.ErrorAs(t, err, &pe)
			default:
				require.NoError(t, err)
				require.Len(t, resp.Hits.Hits, 1)
			}
			require.Len(t, searches, 1)
			require.Contains(t, searches[0].body, tt.wantPIT)
			require.Nil(t, req.Body.PIT, "the caller's request must not be changed")
		})
	}
}

// closeStep is one close call made by TestPITClose.
type closeStep func(t *testing.T, p *opensearchapi.PIT) error

func TestPITClose(t *testing.T) {
	t.Parallel()
	closePIT := closeStep(func(_ *testing.T, p *opensearchapi.PIT) error { return p.Close() })
	cancelled := func(t *testing.T) context.Context {
		t.Helper()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		return ctx
	}
	tests := []struct {
		name                string
		deleteStatuses      []int
		unsuccessfulDeletes int
		maskPITDeletes      bool // the client's error mask hides PitDeleteItems
		cancelOpenCtx       bool
		closes              []closeStep
		wantErrs            []bool // one per close
		wantDeletes         int
	}{
		{
			name:        "a second close is a no-op",
			closes:      []closeStep{closePIT, closePIT},
			wantErrs:    []bool{false, false},
			wantDeletes: 1,
		},
		{
			name:           "a failed delete can be retried",
			deleteStatuses: []int{http.StatusInternalServerError},
			closes:         []closeStep{closePIT, closePIT, closePIT},
			wantErrs:       []bool{true, false, false},
			wantDeletes:    2,
		},
		{
			name:                "a 200 delete reporting successful false is an error and can be retried",
			unsuccessfulDeletes: 1,
			closes:              []closeStep{closePIT, closePIT},
			wantErrs:            []bool{true, false},
			wantDeletes:         2,
		},
		{
			name:                "a successful false delete is an error even when the mask hides it",
			unsuccessfulDeletes: 1,
			maskPITDeletes:      true,
			closes:              []closeStep{closePIT, closePIT},
			wantErrs:            []bool{true, false},
			wantDeletes:         2,
		},
		{
			name:           "not found counts as closed",
			deleteStatuses: []int{http.StatusNotFound},
			closes:         []closeStep{closePIT, closePIT},
			wantErrs:       []bool{false, false},
			wantDeletes:    1,
		},
		{
			name:          "Close still works after the Open context is cancelled",
			cancelOpenCtx: true,
			closes:        []closeStep{closePIT},
			wantErrs:      []bool{false},
			wantDeletes:   1,
		},
		{
			name: "CloseContext uses its context as given, and Close can follow it",
			closes: []closeStep{
				func(t *testing.T, p *opensearchapi.PIT) error {
					t.Helper()
					return p.CloseContext(cancelled(t))
				},
				closePIT,
			},
			wantErrs:    []bool{true, false},
			wantDeletes: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakePITCluster{fakePITConfig: fakePITConfig{deleteStatuses: tt.deleteStatuses, unsuccessfulDeletes: tt.unsuccessfulDeletes}}
			client := newPITClient(t, f)
			if tt.maskPITDeletes {
				require.NoError(t, client.SetErrorMask(client.ErrorMask(), errmask.PitDeleteItems))
			}
			openCtx, cancelOpen := context.WithCancel(t.Context())
			defer cancelOpen()
			pit, err := client.PIT.Open(openCtx, &opensearchapi.CreatePITReq{Indices: []string{"idx"}})
			require.NoError(t, err)
			if tt.cancelOpenCtx {
				cancelOpen()
			}

			for i, closeFn := range tt.closes {
				err := closeFn(t, pit)
				if tt.wantErrs[i] {
					require.Error(t, err, "close %d", i)
					continue
				}
				require.NoError(t, err, "close %d", i)
			}
			dels := f.deletes()
			require.Len(t, dels, tt.wantDeletes)
			for _, d := range dels {
				require.Contains(t, d.body, `"pit-1"`)
			}
		})
	}
}

// TestPITDeletePartialFailure checks the generated PitDeleteItems category on
// client.PIT.Delete: a "successful": false entry is a partial failure unless
// the error mask hides it.
func TestPITDeletePartialFailure(t *testing.T) {
	t.Parallel()
	// deleteFunc sends one kind of PIT delete and returns how many PITs its
	// response lists.
	type deleteFunc func(context.Context, *opensearchapi.Client) (int, error)
	deleteOne := func(ctx context.Context, client *opensearchapi.Client) (int, error) {
		req := &opensearchapi.DeletePITReq{Body: &opensearchapi.DeletePITBody{PITID: []opensearchapi.PITID{pitID("pit-1")}}}
		resp, err := client.PIT.Delete(ctx, req)
		return len(resp.PITs), err
	}
	deleteAll := func(ctx context.Context, client *opensearchapi.Client) (int, error) {
		resp, err := client.PIT.DeleteAll(ctx, &opensearchapi.DeleteAllPITsReq{})
		return len(resp.PITs), err
	}
	tests := []struct {
		name        string
		del         deleteFunc
		mask        bool
		wantPartial bool
	}{
		{name: "Delete reports it by default", del: deleteOne, wantPartial: true},
		{name: "Delete hides it under the mask", del: deleteOne, mask: true},
		{name: "DeleteAll reports it by default", del: deleteAll, wantPartial: true},
		{name: "DeleteAll hides it under the mask", del: deleteAll, mask: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := newPITClient(t, &fakePITCluster{fakePITConfig: fakePITConfig{unsuccessfulDeletes: 1}})
			if tt.mask {
				require.NoError(t, client.SetErrorMask(client.ErrorMask(), errmask.PitDeleteItems))
			}

			pits, err := tt.del(t.Context(), client)
			require.Equal(t, 1, pits, "the response is populated either way")
			if !tt.wantPartial {
				require.NoError(t, err)
				return
			}
			require.True(t, opensearchapi.IsPartialFailure(err))
			pe, ok := errors.AsType[*opensearchapi.PartialPITDeleteError](err)
			require.True(t, ok, "%v", err)
			require.Len(t, pe.Failed, 1)
			require.Equal(t, 0, pe.SucceededCount)
		})
	}
}

func TestPITSearchAfterClose(t *testing.T) {
	t.Parallel()
	f := &fakePITCluster{}
	client := newPITClient(t, f)
	pit, err := client.PIT.Open(t.Context(), &opensearchapi.CreatePITReq{Indices: []string{"idx"}})
	require.NoError(t, err)
	require.NoError(t, pit.Close())

	_, err = pit.Search(t.Context(), &opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{}})
	require.ErrorIs(t, err, opensearchapi.ErrPITClosed)
	require.Empty(t, f.requests("", "/_search"), "a closed PIT must not send a search")
}

func TestPITConcurrentSearchAndClose(t *testing.T) {
	t.Parallel()
	f := &fakePITCluster{}
	client := newPITClient(t, f)
	pit, err := client.PIT.Open(t.Context(), &opensearchapi.CreatePITReq{Indices: []string{"idx"}})
	require.NoError(t, err)

	// require must run on the test goroutine, so the workers only collect errors.
	// A WaitGroup rather than errgroup: the test checks every error, and
	// errgroup keeps only the first.
	var errs struct {
		sync.Mutex
		searches, closes []error
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			_, err := pit.Search(t.Context(), &opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{}})
			errs.Lock()
			errs.searches = append(errs.searches, err)
			errs.Unlock()
		})
		wg.Go(func() {
			err := pit.Close()
			errs.Lock()
			errs.closes = append(errs.closes, err)
			errs.Unlock()
		})
	}
	wg.Wait()
	for _, err := range errs.searches {
		if err != nil {
			require.ErrorIs(t, err, opensearchapi.ErrPITClosed)
		}
	}
	for _, err := range errs.closes {
		require.NoError(t, err)
	}
	require.Len(t, f.deletes(), 1)
}

func TestSearchRespCursor(t *testing.T) {
	t.Parallel()
	sortOf := func(v float64) []opensearchapi.FieldValue {
		return []opensearchapi.FieldValue{opensearchapi.NewFieldValueFromFloat64(v)}
	}
	tests := []struct {
		name   string
		resp   *opensearchapi.SearchResp
		want   opensearchapi.SearchCursor
		wantOK bool
	}{
		{
			name: "a PIT page carries the echoed PIT ID and the last hit's sort values",
			resp: &opensearchapi.SearchResp{
				PITID: new(pitID("pit-9")),
				Hits:  opensearchapi.SearchHitsMetadata{Hits: []opensearchapi.SearchHit{{Sort: sortOf(1)}, {Sort: sortOf(2)}}},
			},
			want:   opensearchapi.SearchCursor{PIT: pitID("pit-9"), After: sortOf(2)},
			wantOK: true,
		},
		{
			name:   "a page without a PIT has no PIT ID",
			resp:   &opensearchapi.SearchResp{Hits: opensearchapi.SearchHitsMetadata{Hits: []opensearchapi.SearchHit{{Sort: sortOf(7)}}}},
			want:   opensearchapi.SearchCursor{After: sortOf(7)},
			wantOK: true,
		},
		{
			name: "a page with no hits has no position",
			resp: &opensearchapi.SearchResp{PITID: new(pitID("pit-9"))},
		},
		{
			name: "a nil response has no position",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := tt.resp.Cursor()
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSearchCursorApply(t *testing.T) {
	t.Parallel()
	after := []opensearchapi.FieldValue{opensearchapi.NewFieldValueFromFloat64(102)}
	tests := []struct {
		name    string
		cur     opensearchapi.SearchCursor
		req     *opensearchapi.SearchReq
		wantPIT *opensearchapi.SearchPointInTimeReference
	}{
		{
			name:    "puts the PIT ID and position in the body",
			cur:     opensearchapi.SearchCursor{PIT: pitID("pit-9"), After: after},
			req:     &opensearchapi.SearchReq{Body: sortedBody(new(3))},
			wantPIT: &opensearchapi.SearchPointInTimeReference{ID: pitID("pit-9")},
		},
		{
			name: "without a PIT only the position is set",
			cur:  opensearchapi.SearchCursor{After: after},
			req:  &opensearchapi.SearchReq{Indices: []string{"idx"}, Body: sortedBody(new(3))},
		},
		{
			name:    "creates the body when the request has none",
			cur:     opensearchapi.SearchCursor{PIT: pitID("pit-9"), After: after},
			req:     &opensearchapi.SearchReq{},
			wantPIT: &opensearchapi.SearchPointInTimeReference{ID: pitID("pit-9")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var bodyBefore *opensearchapi.SearchBody
			if tt.req.Body != nil {
				b := *tt.req.Body
				bodyBefore = &b
			}

			got := tt.cur.Apply(tt.req)

			require.Equal(t, tt.wantPIT, got.Body.PIT)
			require.Equal(t, after, got.Body.SearchAfter)
			require.Equal(t, tt.req.Indices, got.Indices)
			require.Equal(t, bodyBefore, tt.req.Body, "the caller's request must not be changed")
		})
	}
}

func TestParsePITID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{name: "an ID", in: "o463QQEPbXktaW5kZXgtMDAwMDAx"},
		{name: "empty", in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			id, err := opensearchapi.ParsePITID(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				require.True(t, id.IsZero())
				return
			}
			require.NoError(t, err)
			require.False(t, id.IsZero())
			require.Equal(t, tt.in, id.String())
		})
	}
}

// TestPITIDJSON pins the wire form: a PITID encodes as the bare string in
// every field shape that carries one.
func TestPITIDJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		v    any
		json string
	}{
		{name: "search body", v: &opensearchapi.SearchPointInTimeReference{ID: pitID("pit-9")}, json: `{"id":"pit-9"}`},
		{
			name: "delete body",
			v:    &opensearchapi.DeletePITBody{PITID: []opensearchapi.PITID{pitID("a"), pitID("b")}},
			json: `{"pit_id":["a","b"]}`,
		},
		{name: "create response", v: &opensearchapi.CreatePITResp{PITID: new(pitID("pit-9"))}, json: `{"pit_id":"pit-9"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(tt.v)
			require.NoError(t, err)
			require.JSONEq(t, tt.json, string(got))

			back := reflect.New(reflect.TypeOf(tt.v).Elem()).Interface()
			require.NoError(t, json.Unmarshal([]byte(tt.json), back))
			require.Equal(t, tt.v, back)
		})
	}
}

// TestSearchCursorJSON stores a cursor the way a service handing it to its
// caller would, and gets the same cursor back.
func TestSearchCursorJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cur  opensearchapi.SearchCursor
		json string
	}{
		{
			name: "with a PIT",
			cur: opensearchapi.SearchCursor{
				PIT:   pitID("pit-9"),
				After: []opensearchapi.FieldValue{opensearchapi.NewFieldValueFromFloat64(102), opensearchapi.NewFieldValueFromString("b")},
			},
			json: `{"pit":"pit-9","search_after":[102,"b"]}`,
		},
		{
			name: "without a PIT",
			cur:  opensearchapi.SearchCursor{After: []opensearchapi.FieldValue{opensearchapi.NewFieldValueFromFloat64(7)}},
			json: `{"search_after":[7]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data, err := json.Marshal(tt.cur)
			require.NoError(t, err)
			require.JSONEq(t, tt.json, string(data))
			var back opensearchapi.SearchCursor
			require.NoError(t, json.Unmarshal(data, &back))
			require.Equal(t, tt.cur.PIT, back.PIT)
			again, err := json.Marshal(back)
			require.NoError(t, err)
			require.JSONEq(t, tt.json, string(again), "a decoded FieldValue keeps its raw bytes, so compare the encoding")
		})
	}
}

// TestClientSearchAfterPagesResumesFromCursor pages two requests the way a
// service would: the first page's cursor, applied to the next request, resumes
// the PIT where the first page stopped.
func TestClientSearchAfterPagesResumesFromCursor(t *testing.T) {
	t.Parallel()
	f := &fakePITCluster{fakePITConfig: fakePITConfig{pages: 3, hitsPerPage: 3}}
	client := newPITClient(t, f)
	pit, err := client.PIT.Open(t.Context(), &opensearchapi.CreatePITReq{Indices: []string{"idx"}})
	require.NoError(t, err)

	first, err := pit.Search(t.Context(), &opensearchapi.SearchReq{Body: sortedBody(new(3))})
	require.NoError(t, err)
	cur, ok := first.Cursor()
	require.True(t, ok)
	require.Equal(t, pit.ID(), cur.PIT)

	pages, errf := client.SearchAfterPages(t.Context(), cur.Apply(&opensearchapi.SearchReq{Body: sortedBody(new(3))}))
	var ids []string
	for resp := range pages {
		for _, h := range resp.Hits.Hits {
			ids = append(ids, *h.ID)
		}
	}
	require.NoError(t, errf())
	require.Equal(t, []string{"2-0", "2-1", "2-2", "3-0", "3-1", "3-2"}, ids)
	searches := f.requests("", "/_search")
	require.Contains(t, searches[1].body, `"pit":{"id":"pit-1"}`)
	require.Contains(t, searches[1].body, `"search_after":[102]`)
}

func TestClientSearchAfterPages(t *testing.T) {
	t.Parallel()
	withPIT := func(b *opensearchapi.SearchBody) *opensearchapi.SearchBody {
		b.PIT = &opensearchapi.SearchPointInTimeReference{ID: pitID("pit-9")}
		return b
	}
	tests := []struct {
		name          string
		req           *opensearchapi.SearchReq
		wantPages     int
		wantSearches  int
		wantPath      string   // path of every search
		wantInBody    []string // in the first search's body
		wantNotInBody string   // absent from every search's body
		wantErr       string
	}{
		{
			name:          "pages the indices without a PIT",
			req:           &opensearchapi.SearchReq{Indices: []string{"idx"}, Body: sortedBody(new(3))},
			wantPages:     2,
			wantSearches:  3,
			wantPath:      "/idx/_search",
			wantNotInBody: `"pit"`,
		},
		{
			name: "resumes a PIT from the caller's position",
			req: &opensearchapi.SearchReq{Body: func() *opensearchapi.SearchBody {
				b := withPIT(sortedBody(new(3)))
				b.SearchAfter = []opensearchapi.FieldValue{opensearchapi.NewFieldValueFromFloat64(102)} // page 1's last hit
				return b
			}()},
			wantPages:    1,
			wantSearches: 2,
			wantPath:     "/_search",
			wantInBody:   []string{`"pit":{"id":"pit-9"}`, `"search_after":[102]`},
		},
		{
			name:    "a PIT with indices fails before any search",
			req:     &opensearchapi.SearchReq{Indices: []string{"idx"}, Body: withPIT(sortedBody(new(3)))},
			wantErr: "Indices",
		},
		{
			name:    "a missing sort fails before any search",
			req:     &opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{}},
			wantErr: "Sort",
		},
		{
			name:    "a raw body fails before any search",
			req:     &opensearchapi.SearchReq{BodyReader: strings.NewReader(`{}`)},
			wantErr: "Body",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakePITCluster{fakePITConfig: fakePITConfig{pages: 2, hitsPerPage: 3}}
			client := newPITClient(t, f)

			pages, errf := client.SearchAfterPages(t.Context(), tt.req)
			got := 0
			for range pages {
				got++
			}

			searches := f.requests("", "/_search")
			require.Empty(t, f.creates(), "the client iterators never create a PIT")
			require.Empty(t, f.deletes(), "the client iterators never delete a PIT")
			if tt.wantErr != "" {
				require.ErrorContains(t, errf(), tt.wantErr)
				require.Empty(t, searches, "an invalid request must not be sent")
				return
			}
			require.NoError(t, errf())
			require.Equal(t, tt.wantPages, got)
			require.Len(t, searches, tt.wantSearches)
			for _, s := range searches {
				require.Equal(t, tt.wantPath, s.path)
				if tt.wantNotInBody != "" {
					require.NotContains(t, s.body, tt.wantNotInBody)
				}
			}
			for _, want := range tt.wantInBody {
				require.Contains(t, searches[0].body, want)
			}
		})
	}
}

// TestClientSearchAfter checks the hits iterator yields every page's hits in
// order and reports the scan's error through its func.
func TestClientSearchAfter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     fakePITConfig
		wantIDs []string
		wantErr bool
	}{
		{
			name:    "flattens every page",
			cfg:     fakePITConfig{pages: 2, hitsPerPage: 3},
			wantIDs: []string{"1-0", "1-1", "1-2", "2-0", "2-1", "2-2"},
		},
		{
			name:    "stops at a failed page",
			cfg:     fakePITConfig{pages: 3, hitsPerPage: 3, failSearchOnPage: 2},
			wantIDs: []string{"1-0", "1-1", "1-2"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := newPITClient(t, &fakePITCluster{fakePITConfig: tt.cfg})

			hits, errf := client.SearchAfter(t.Context(), &opensearchapi.SearchReq{Indices: []string{"idx"}, Body: sortedBody(new(3))})
			var ids []string
			for h := range hits {
				ids = append(ids, *h.ID)
			}
			require.Equal(t, tt.wantIDs, ids)
			if tt.wantErr {
				require.Error(t, errf())
				return
			}
			require.NoError(t, errf())
		})
	}
}

func TestSearchContextMissing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		status      int
		rootCause   string
		wantMissing bool
	}{
		{name: "an expired or deleted PIT", status: http.StatusNotFound, rootCause: "search_context_missing_exception", wantMissing: true},
		{name: "a missing index is not a missing context", status: http.StatusNotFound, rootCause: "index_not_found_exception"},
		{name: "a malformed PIT ID is not a missing context", status: http.StatusInternalServerError, rootCause: "unsupported_version_exception"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakePITCluster{fakePITConfig: fakePITConfig{searchErrStatus: tt.status, searchErrType: tt.rootCause}}
			client := newPITClient(t, f)
			pit, err := client.PIT.Open(t.Context(), &opensearchapi.CreatePITReq{Indices: []string{"idx"}})
			require.NoError(t, err)

			_, searchErr := client.Search(t.Context(), &opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{
				PIT: &opensearchapi.SearchPointInTimeReference{ID: pit.ID()},
			}})
			_, pitSearchErr := pit.Search(t.Context(), &opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{}})
			pages, errf := pit.SearchAfterPages(t.Context(), &opensearchapi.SearchReq{Body: sortedBody(new(3))})
			for range pages {
				require.Fail(t, "no page must be yielded")
			}
			for _, err := range []error{searchErr, pitSearchErr, errf()} {
				require.Error(t, err)
				_, missing := errors.AsType[*opensearchapi.SearchContextMissingError](err)
				require.Equal(t, tt.wantMissing, missing, "%v", err)
				se, ok := errors.AsType[*opensearch.StructError](err)
				require.True(t, ok, "the server's error must stay reachable: %v", err)
				require.Equal(t, tt.status, se.Status)
			}
		})
	}
}

// sortedBody returns a search body sorted on _doc, the minimal sort the
// iterators accept.
func sortedBody(size *int) *opensearchapi.SearchBody {
	sort := opensearchapi.NewSortFromArray([]opensearchapi.SortCombinations{opensearchapi.NewSortCombinationsFromString("_doc")})
	return &opensearchapi.SearchBody{Sort: &sort, Size: size}
}

func TestPITSearchAfterPages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		cluster         fakePITConfig
		maskShards      bool
		req             *opensearchapi.SearchReq
		closeFirst      bool
		breakAfterPages int // 0 means read everything
		wantPages       int
		wantSearches    int
		wantSearchAfter []string // search_after sent on each search after the first
		wantResumeFrom  string   // search_after sent on the first search
		wantSize        string   // size sent on the first search
		wantErr         string
		wantErrIs       error
		wantPartial     bool
	}{
		{
			name:            "pages until a short page and hands the last sort to the next page",
			cluster:         fakePITConfig{pages: 2, hitsPerPage: 3},
			req:             &opensearchapi.SearchReq{Body: sortedBody(new(3))},
			wantPages:       2,
			wantSearches:    3,
			wantSearchAfter: []string{`"search_after":[102]`, `"search_after":[202]`},
			wantSize:        `"size":3`,
		},
		{
			name:    "resumes from the caller's search_after",
			cluster: fakePITConfig{pages: 5, hitsPerPage: 3},
			req: &opensearchapi.SearchReq{Body: func() *opensearchapi.SearchBody {
				b := sortedBody(new(3))
				b.SearchAfter = []opensearchapi.FieldValue{opensearchapi.NewFieldValueFromFloat64(202)} // page 2's last hit
				return b
			}()},
			wantPages:       3,
			wantSearches:    4,
			wantResumeFrom:  `"search_after":[202]`,
			wantSearchAfter: []string{`"search_after":[302]`, `"search_after":[402]`, `"search_after":[502]`},
		},
		{
			name:         "a first page shorter than size is the last",
			cluster:      fakePITConfig{pages: 5, hitsPerPage: 3},
			req:          &opensearchapi.SearchReq{Body: sortedBody(new(10))},
			wantPages:    1,
			wantSearches: 1,
			wantSize:     `"size":10`,
		},
		{
			name:         "size defaults to 1000",
			cluster:      fakePITConfig{pages: 1, hitsPerPage: 3},
			req:          &opensearchapi.SearchReq{Body: sortedBody(nil)},
			wantPages:    1,
			wantSearches: 1,
			wantSize:     `"size":1000`,
		},
		{
			name:            "an early break stops paging",
			cluster:         fakePITConfig{pages: 5, hitsPerPage: 3},
			req:             &opensearchapi.SearchReq{Body: sortedBody(new(3))},
			breakAfterPages: 1,
			wantPages:       1,
			wantSearches:    1,
		},
		{
			name:         "a masked shard failure still stops, without yielding that page",
			cluster:      fakePITConfig{pages: 5, hitsPerPage: 3, failShardOnPage: 2},
			maskShards:   true,
			req:          &opensearchapi.SearchReq{Body: sortedBody(new(3))},
			wantPages:    1,
			wantSearches: 2,
			wantPartial:  true,
		},
		{
			name:         "an unmasked shard failure stops, without yielding that page",
			cluster:      fakePITConfig{pages: 5, hitsPerPage: 3, failShardOnPage: 2},
			req:          &opensearchapi.SearchReq{Body: sortedBody(new(3))},
			wantPages:    1,
			wantSearches: 2,
			wantPartial:  true,
		},
		{
			name:    "a missing sort fails before any search",
			cluster: fakePITConfig{pages: 1, hitsPerPage: 3},
			req:     &opensearchapi.SearchReq{Body: &opensearchapi.SearchBody{}},
			wantErr: "Sort",
		},
		{
			name:    "a zero size fails before any search",
			cluster: fakePITConfig{pages: 1, hitsPerPage: 3},
			req:     &opensearchapi.SearchReq{Body: sortedBody(new(0))},
			wantErr: "Size",
		},
		{
			name:    "a negative size fails before any search",
			cluster: fakePITConfig{pages: 1, hitsPerPage: 3},
			req:     &opensearchapi.SearchReq{Body: sortedBody(new(-1))},
			wantErr: "Size",
		},
		{
			name:    "a raw body next to a typed one fails before any search",
			cluster: fakePITConfig{pages: 1, hitsPerPage: 3},
			req:     &opensearchapi.SearchReq{Body: sortedBody(new(3)), BodyReader: strings.NewReader(`{}`)},
			wantErr: "BodyReader",
		},
		{
			name:         "a search error mid-scan stops, after yielding the pages before it",
			cluster:      fakePITConfig{pages: 5, hitsPerPage: 3, failSearchOnPage: 2},
			req:          &opensearchapi.SearchReq{Body: sortedBody(new(3))},
			wantPages:    1,
			wantSearches: 2,
			wantErr:      "search failed",
		},
		{
			name:    "indices fail before any search",
			cluster: fakePITConfig{pages: 1, hitsPerPage: 3},
			req:     &opensearchapi.SearchReq{Indices: []string{"idx"}, Body: sortedBody(new(3))},
			wantErr: "Indices",
		},
		{
			name:       "a closed PIT reports ErrPITClosed",
			cluster:    fakePITConfig{pages: 1, hitsPerPage: 3},
			req:        &opensearchapi.SearchReq{Body: sortedBody(new(3))},
			closeFirst: true,
			wantErrIs:  opensearchapi.ErrPITClosed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakePITCluster{fakePITConfig: tt.cluster}
			client := newPITClient(t, f)
			if tt.maskShards {
				require.NoError(t, client.SetErrorMask(client.ErrorMask(), errmask.SearchShards))
			}
			pit, err := client.PIT.Open(t.Context(), &opensearchapi.CreatePITReq{Indices: []string{"idx"}})
			require.NoError(t, err)
			if tt.closeFirst {
				require.NoError(t, pit.Close())
			}
			deletesBefore := len(f.deletes())
			var sizeBefore *int
			var searchAfterBefore []opensearchapi.FieldValue
			if tt.req.Body != nil {
				sizeBefore = tt.req.Body.Size
				searchAfterBefore = tt.req.Body.SearchAfter
			}

			pages, errf := pit.SearchAfterPages(t.Context(), tt.req)
			got := 0
			for resp := range pages {
				require.NotEmpty(t, resp.Hits.Hits, "empty pages must not be yielded")
				got++
				if got == tt.breakAfterPages {
					break
				}
			}

			searches := f.requests("", "/_search")
			require.Equal(t, tt.wantPages, got)
			require.Len(t, searches, tt.wantSearches)
			for i, want := range tt.wantSearchAfter {
				require.Contains(t, searches[i+1].body, want)
			}
			if tt.wantSize != "" {
				require.Contains(t, searches[0].body, tt.wantSize)
			}
			if tt.wantResumeFrom != "" {
				require.Contains(t, searches[0].body, tt.wantResumeFrom)
			}
			require.Len(t, f.deletes(), deletesBefore, "the handle's iterators must not close the PIT")
			if tt.req.Body != nil {
				require.Nil(t, tt.req.Body.PIT, "the caller's request must not be changed")
				require.Equal(t, searchAfterBefore, tt.req.Body.SearchAfter, "the caller's request must not be changed")
				require.Equal(t, sizeBefore, tt.req.Body.Size, "the caller's request must not be changed")
			}

			err = errf()
			switch {
			case tt.wantErr != "":
				require.ErrorContains(t, err, tt.wantErr)
			case tt.wantErrIs != nil:
				require.ErrorIs(t, err, tt.wantErrIs)
			case tt.wantPartial:
				var pe *opensearchapi.PartialSearchError
				require.ErrorAs(t, err, &pe)
				require.Equal(t, 1, pe.FailedShards)
			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestPITSearchAfter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		breakAfter   int // hits; 0 means read everything
		wantHits     int
		wantSearches int
	}{
		{name: "yields every hit across pages", wantHits: 6, wantSearches: 3},
		{name: "an early break mid-page stops paging", breakAfter: 4, wantHits: 4, wantSearches: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakePITCluster{fakePITConfig: fakePITConfig{pages: 2, hitsPerPage: 3}}
			client := newPITClient(t, f)
			pit, err := client.PIT.Open(t.Context(), &opensearchapi.CreatePITReq{Indices: []string{"idx"}})
			require.NoError(t, err)

			hits, errf := pit.SearchAfter(t.Context(), &opensearchapi.SearchReq{Body: sortedBody(new(3))})
			got := 0
			for range hits {
				got++
				if got == tt.breakAfter {
					break
				}
			}
			require.NoError(t, errf())
			require.Equal(t, tt.wantHits, got)
			require.Len(t, f.requests("", "/_search"), tt.wantSearches)
		})
	}
}

func TestPointInTimeClientSearchAfterPages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		cluster         fakePITConfig
		body            *opensearchapi.SearchBody
		breakAfterPages int // 0 means read everything
		wantPages       int
		wantCreates     int
		wantDeletes     int
		wantErrs        []string // substrings the error must contain
		wantPartial     bool
	}{
		{
			name:        "reads everything and deletes the PIT",
			cluster:     fakePITConfig{pages: 2, hitsPerPage: 3},
			body:        sortedBody(new(3)),
			wantPages:   2,
			wantCreates: 1,
			wantDeletes: 1,
		},
		{
			name:            "an early break still deletes the PIT",
			cluster:         fakePITConfig{pages: 5, hitsPerPage: 3},
			body:            sortedBody(new(3)),
			breakAfterPages: 1,
			wantPages:       1,
			wantCreates:     1,
			wantDeletes:     1,
		},
		{
			name:            "a failed delete after an early break is reported",
			cluster:         fakePITConfig{pages: 5, hitsPerPage: 3, deleteStatuses: []int{http.StatusInternalServerError}},
			body:            sortedBody(new(3)),
			breakAfterPages: 1,
			wantPages:       1,
			wantCreates:     1,
			wantDeletes:     1,
			wantErrs:        []string{"delete PIT"},
		},
		{
			name:        "a failed shard deletes the PIT and reports both errors",
			cluster:     fakePITConfig{pages: 5, hitsPerPage: 3, failShardOnPage: 2, deleteStatuses: []int{http.StatusInternalServerError}},
			body:        sortedBody(new(3)),
			wantPages:   1,
			wantCreates: 1,
			wantDeletes: 1,
			wantErrs:    []string{"delete PIT"},
			wantPartial: true,
		},
		{
			name:     "a create error opens nothing to delete",
			cluster:  fakePITConfig{pages: 1, hitsPerPage: 3, createStatus: http.StatusBadRequest},
			body:     sortedBody(new(3)),
			wantErrs: []string{"create failed"},
			// the create was attempted but no PIT exists, so nothing is deleted
			wantCreates: 1,
		},
		{
			name:     "an invalid request fails before a PIT is created",
			cluster:  fakePITConfig{pages: 1, hitsPerPage: 3},
			body:     &opensearchapi.SearchBody{},
			wantErrs: []string{"Sort"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakePITCluster{fakePITConfig: tt.cluster}
			client := newPITClient(t, f)

			pages, errf := client.PIT.SearchAfterPages(t.Context(),
				&opensearchapi.CreatePITReq{Indices: []string{"idx"}},
				&opensearchapi.SearchReq{Body: tt.body})
			got := 0
			for range pages {
				got++
				if got == tt.breakAfterPages {
					break
				}
			}

			require.Equal(t, tt.wantPages, got)
			require.Len(t, f.creates(), tt.wantCreates)
			require.Len(t, f.deletes(), tt.wantDeletes)
			err := errf()
			if len(tt.wantErrs) == 0 && !tt.wantPartial {
				require.NoError(t, err)
				return
			}
			for _, want := range tt.wantErrs {
				require.ErrorContains(t, err, want)
			}
			if tt.wantPartial {
				var pe *opensearchapi.PartialSearchError
				require.ErrorAs(t, err, &pe)
			}
		})
	}
}

// TestPointInTimeClientSearchAfterPagesCleanup checks the one-shot deletes its
// PIT when the loop body panics or cancels the scan's context.
func TestPointInTimeClientSearchAfterPagesCleanup(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		afterPage func(cancel context.CancelFunc) // runs in the loop body for each page
		wantPanic bool
		wantErrIs error
	}{
		{name: "a panic in the loop body", afterPage: func(context.CancelFunc) { panic("boom") }, wantPanic: true},
		{name: "a cancelled context", afterPage: func(cancel context.CancelFunc) { cancel() }, wantErrIs: context.Canceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakePITCluster{fakePITConfig: fakePITConfig{pages: 5, hitsPerPage: 3}}
			client := newPITClient(t, f)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			pages, errf := client.PIT.SearchAfterPages(ctx,
				&opensearchapi.CreatePITReq{Indices: []string{"idx"}},
				&opensearchapi.SearchReq{Body: sortedBody(new(3))})
			scan := func() {
				for range pages {
					tt.afterPage(cancel)
				}
			}
			if tt.wantPanic {
				require.PanicsWithValue(t, "boom", scan)
			} else {
				scan()
				require.ErrorIs(t, errf(), tt.wantErrIs)
			}
			require.Len(t, f.deletes(), 1, "the PIT must be deleted")
		})
	}
}

func TestPointInTimeClientSearchAfter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		cluster    fakePITConfig
		breakAfter int // hits; 0 means read everything
		wantHits   int
		wantErr    string
	}{
		{name: "reads every hit and deletes the PIT", cluster: fakePITConfig{pages: 2, hitsPerPage: 3}, wantHits: 6},
		{
			name:       "a failed delete after an early break is reported",
			cluster:    fakePITConfig{pages: 5, hitsPerPage: 3, deleteStatuses: []int{http.StatusInternalServerError}},
			breakAfter: 4,
			wantHits:   4,
			wantErr:    "delete PIT",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakePITCluster{fakePITConfig: tt.cluster}
			client := newPITClient(t, f)

			hits, errf := client.PIT.SearchAfter(t.Context(),
				&opensearchapi.CreatePITReq{Indices: []string{"idx"}},
				&opensearchapi.SearchReq{Body: sortedBody(new(3))})
			got := 0
			for range hits {
				got++
				if got == tt.breakAfter {
					break
				}
			}
			require.Equal(t, tt.wantHits, got)
			require.Len(t, f.deletes(), 1)
			if tt.wantErr == "" {
				require.NoError(t, errf())
				return
			}
			require.ErrorContains(t, errf(), tt.wantErr)
		})
	}
}

func TestPITSearchAfterPagesRetry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		cluster      fakePITConfig
		wantPages    int
		wantSearches int
		wantErrIs    error
		wantPartial  bool
		wantErrText  []string // substrings of the stop error
	}{
		{
			name: "a rejected shard is retried and the scan recovers",
			cluster: fakePITConfig{
				pages: 2, hitsPerPage: 3, failShardOnPage: 2, failShardTimes: 1, failShardReason: "rejected_execution_exception",
			},
			wantPages:    2,
			wantSearches: 4, // page 1, page 2 failed, page 2 retried, empty page 3
		},
		{
			name: "the es_ spelling of a rejection is retried too",
			cluster: fakePITConfig{
				pages: 2, hitsPerPage: 3, failShardOnPage: 2, failShardTimes: 1, failShardReason: "es_rejected_execution_exception",
			},
			wantPages:    2,
			wantSearches: 4,
		},
		{
			name: "a rejection in caused_by is retried",
			cluster: fakePITConfig{
				pages: 2, hitsPerPage: 3, failShardOnPage: 2, failShardTimes: 1,
				failShardReason: "remote_transport_exception", failShardCausedBy: "rejected_execution_exception",
			},
			wantPages:    2,
			wantSearches: 4,
		},
		{
			name:         "a rejection that persists stops after two retries",
			cluster:      fakePITConfig{pages: 5, hitsPerPage: 3, failShardOnPage: 2, failShardReason: "rejected_execution_exception"},
			wantPages:    1,
			wantSearches: 4, // page 1, then three attempts at page 2
			wantPartial:  true,
			wantErrText:  []string{"page 2", "3 hits"},
		},
		{
			name:         "a failure that is not retryable stops at once",
			cluster:      fakePITConfig{pages: 5, hitsPerPage: 3, failShardOnPage: 2, failShardReason: "query_shard_exception"},
			wantPages:    1,
			wantSearches: 2,
			wantPartial:  true,
			wantErrText:  []string{"page 2", "3 hits"},
		},
		{
			name:         "a timed-out page is retried and the scan recovers",
			cluster:      fakePITConfig{pages: 2, hitsPerPage: 3, timedOutOnPage: 2, timedOutTimes: 1},
			wantPages:    2,
			wantSearches: 4,
		},
		{
			name:         "a page that keeps timing out stops after two retries",
			cluster:      fakePITConfig{pages: 5, hitsPerPage: 3, timedOutOnPage: 2},
			wantPages:    1,
			wantSearches: 4,
			wantErrIs:    opensearchapi.ErrSearchPageIncomplete,
			wantErrText:  []string{"page 2", "timed out"},
		},
		{
			name:         "a page that terminated early stops at once",
			cluster:      fakePITConfig{pages: 5, hitsPerPage: 3, terminatedEarlyOnPage: 1},
			wantPages:    0,
			wantSearches: 1,
			wantErrIs:    opensearchapi.ErrSearchPageIncomplete,
			wantErrText:  []string{"page 1", "0 hits", "terminated early"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := &fakePITCluster{fakePITConfig: tt.cluster}
			client := newPITClient(t, f)
			// Masked, so a failed shard reaches the iterator as data rather than as a Search error.
			require.NoError(t, client.SetErrorMask(client.ErrorMask(), errmask.SearchShards))
			pit, err := client.PIT.Open(t.Context(), &opensearchapi.CreatePITReq{Indices: []string{"idx"}})
			require.NoError(t, err)

			pages, errf := pit.SearchAfterPages(t.Context(), &opensearchapi.SearchReq{Body: sortedBody(new(3))})
			got := 0
			for range pages {
				got++
			}
			require.Equal(t, tt.wantPages, got)
			require.Len(t, f.requests("", "/_search"), tt.wantSearches)

			err = errf()
			if tt.wantErrIs == nil && !tt.wantPartial {
				require.NoError(t, err)
				return
			}
			if tt.wantErrIs != nil {
				require.ErrorIs(t, err, tt.wantErrIs)
			}
			if tt.wantPartial {
				var pe *opensearchapi.PartialSearchError
				require.ErrorAs(t, err, &pe)
			}
			for _, want := range tt.wantErrText {
				require.ErrorContains(t, err, want)
			}
		})
	}
}

func TestPITSearchAfterPagesRetryHonorsContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Cancel as soon as page 2 fails, so the retry backoff is what sees it.
	f := &fakePITCluster{fakePITConfig: fakePITConfig{
		pages: 5, hitsPerPage: 3, failShardOnPage: 2, failShardReason: "rejected_execution_exception",
		onSearch: func(page, _ int) {
			if page == 2 {
				cancel()
			}
		},
	}}
	client := newPITClient(t, f)
	require.NoError(t, client.SetErrorMask(client.ErrorMask(), errmask.SearchShards))
	pit, err := client.PIT.Open(t.Context(), &opensearchapi.CreatePITReq{Indices: []string{"idx"}})
	require.NoError(t, err)

	pages, errf := pit.SearchAfterPages(ctx, &opensearchapi.SearchReq{Body: sortedBody(new(3))})
	for range pages {
	}
	require.ErrorIs(t, errf(), context.Canceled)
	require.Len(t, f.requests("", "/_search"), 2, "a cancelled context must stop retries")
}

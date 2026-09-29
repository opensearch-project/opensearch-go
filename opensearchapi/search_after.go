// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"
)

// defaultSearchAfterPageSize is the page size the search_after iterators use
// when the search body leaves Size unset. Every page is one round trip across
// all of the search's shards, so pages should be large; 1000 stays well under
// the default index.max_result_window of 10000 and keeps each response to a
// size a caller can hold in memory.
const defaultSearchAfterPageSize = 1000

// searchAfterPageRetries is how many extra attempts the search_after iterators
// make at a page whose failure is transient, a rejected shard or a timeout. Two
// retries ride out a brief spike in a full search queue while a sick cluster
// still fails fast.
const searchAfterPageRetries = 2

// searchAfterRetryBackoff is the wait before the first retry of a page; each
// later retry waits four times longer, so two retries wait 100ms, then 400ms.
// That is long enough for a full search queue to drain a little and short
// enough that a scan against a sick cluster still fails within a second.
const searchAfterRetryBackoff = 100 * time.Millisecond

// ErrSearchPageIncomplete is returned by the search_after iterators when a page
// came back incomplete without a failed shard: the search timed out or
// terminated early. Paging past it would skip documents, so the scan stops.
var ErrSearchPageIncomplete = errors.New("opensearchapi: search page is incomplete")

// searchFunc runs one search. The PIT handle's iterators pass [PIT.Search], so
// every page checks that the handle is still open.
type searchFunc func(context.Context, *SearchReq) (*SearchResp, error)

// SearchAfterPages pages req with search_after and yields each page that has
// hits. req.Body.Sort must be set and should end in a unique tiebreaker, or
// pages can skip or repeat documents. Size defaults to 1000; paging stops at
// the first page with fewer hits than that. A Body.SearchAfter set by the
// caller is where the first page starts, so a scan can be resumed from the
// last hit's sort values.
//
// Without a PIT the scan pages the live req.Indices, so documents indexed or
// deleted during it can shift the pages. With req.Body.PIT set it pages that
// PIT's snapshot and req.Indices must be empty; the iterators never create or
// delete a PIT. [PIT.SearchAfterPages] and [PointInTimeClient.SearchAfterPages]
// manage one for you.
//
// Call the returned func after the loop to get any error, as with
// [bufio.Scanner.Err]. The error names the page that stopped the scan and how
// many hits were yielded before it.
//
// An incomplete page is never yielded, because search_after would move past
// its missing documents for good. A page with a failed shard stops the scan
// with a [*PartialSearchError], even when the client's error mask hides partial
// failures; one that timed out or terminated early stops it with
// [ErrSearchPageIncomplete]. A page whose failed shards were all rejected by a
// full search queue, or that timed out, is retried twice with a short backoff
// first. search_after keeps the position, so a retried page does not skip or
// repeat documents; without a PIT it can see documents indexed in between.
func (c Client) SearchAfterPages(ctx context.Context, req *SearchReq) (iter.Seq[*SearchResp], func() error) {
	if err := checkPagingReq(req); err != nil {
		return func(func(*SearchResp) bool) {}, func() error { return err }
	}
	return searchAfterPages(ctx, req, c.Search)
}

// SearchAfter is [Client.SearchAfterPages] flattened to hits.
func (c Client) SearchAfter(ctx context.Context, req *SearchReq) (iter.Seq[SearchHit], func() error) {
	pages, errf := c.SearchAfterPages(ctx, req)
	return flattenHits(pages), errf
}

// SearchCursor is where a search_after scan stands between requests: the PIT
// it pages, if any, and the sort values of the last hit it returned. Read it
// from a page with [SearchResp.Cursor], keep it between requests, and resume
// with [SearchCursor.Apply]. It round-trips through encoding/json, so it can be
// stored or handed to another process whole. It carries the raw PIT ID, so see
// [PIT] before exposing it outside your service.
type SearchCursor struct {
	PIT   PITID        `json:"pit,omitzero"` // zero when the scan pages live indices
	After []FieldValue `json:"search_after"`
}

// Cursor returns where r leaves off: the PIT ID the server echoes on every PIT
// search, and the last hit's sort values. ok is false when r has no hits, as
// on the last page of a scan.
func (r *SearchResp) Cursor() (SearchCursor, bool) {
	if r == nil || len(r.Hits.Hits) == 0 {
		return SearchCursor{}, false
	}
	var cur SearchCursor
	if r.PITID != nil {
		cur.PIT = *r.PITID
	}
	cur.After = r.Hits.Hits[len(r.Hits.Hits)-1].Sort
	return cur, true
}

// Apply returns a copy of req set to continue from c: c.PIT goes into
// Body.PIT and c.After into Body.SearchAfter, the only places OpenSearch takes
// them. req is not modified. With a PIT, req.Indices must be empty, because
// the PIT pins them.
func (c SearchCursor) Apply(req *SearchReq) *SearchReq {
	next := *req
	var body SearchBody
	if req.Body != nil {
		body = *req.Body
	}
	if !c.PIT.IsZero() {
		body.PIT = &SearchPointInTimeReference{ID: c.PIT}
	}
	body.SearchAfter = c.After
	next.Body = &body
	return &next
}

// searchAfterPages is the paging loop behind every search_after iterator. req
// must already have passed checkPagingReq.
func searchAfterPages(ctx context.Context, req *SearchReq, search searchFunc) (iter.Seq[*SearchResp], func() error) {
	var err error
	seq := func(yield func(*SearchResp) bool) {
		body := *req.Body
		if body.Size == nil {
			body.Size = new(defaultSearchAfterPageSize)
		}
		page := *req
		page.Body = &body
		yielded := 0
		for n := 1; ; n++ {
			resp, perr := searchPage(ctx, &page, search)
			if perr != nil {
				err = fmt.Errorf("opensearchapi: search_after page %d (%d hits yielded before it): %w", n, yielded, perr)
				return
			}
			hits := resp.Hits.Hits
			if len(hits) > 0 {
				if !yield(resp) {
					return
				}
				yielded += len(hits)
			}
			if len(hits) < *body.Size {
				return
			}
			body.SearchAfter = hits[len(hits)-1].Sort
		}
	}
	return seq, func() error { return err }
}

// searchPage runs one page of a scan, retrying it while its problem is
// transient. It returns the page only when it came back complete.
func searchPage(ctx context.Context, req *SearchReq, search searchFunc) (*SearchResp, error) {
	for attempt := 0; ; attempt++ {
		resp, err := search(ctx, req)
		retryable, problem := pageProblem(resp, err)
		if problem == nil {
			return resp, nil
		}
		if !retryable || attempt == searchAfterPageRetries {
			return nil, problem
		}
		if werr := waitCtx(ctx, searchAfterRetryBackoff<<(2*attempt)); werr != nil {
			return nil, werr
		}
	}
}

// pageProblem reports what made a page incomplete, if anything, and whether
// running the page again could fix it. A Search error with no failed shard,
// such as a transport or HTTP error, is returned as is and not retried here:
// the transport has its own retries.
func pageProblem(resp *SearchResp, err error) (bool, error) {
	if err != nil && (resp == nil || resp.Shards.Failed == 0) {
		return false, err
	}
	if failure := resp.SearchShardFailures(); failure != nil {
		return allRejected(failure.Failures), failure
	}
	switch {
	case resp.TimedOut:
		return true, fmt.Errorf("%w: the search timed out", ErrSearchPageIncomplete)
	case resp.TerminatedEarly != nil && *resp.TerminatedEarly:
		return false, fmt.Errorf("%w: the search terminated early", ErrSearchPageIncomplete)
	}
	return false, nil
}

// allRejected reports whether every shard failure was a rejection by a full
// search queue, the one kind of shard failure that retrying can clear.
func allRejected(failures []ShardSearchFailure) bool {
	if len(failures) == 0 {
		return false
	}
	for _, f := range failures {
		if !isRejection(&f.Reason) {
			return false
		}
	}
	return true
}

// Error types OpenSearch reports for a search rejected by a full search queue;
// the es_ form is the older spelling.
const (
	rejectedExecutionType   = "rejected_execution_exception"
	esRejectedExecutionType = "es_rejected_execution_exception"
)

// isRejection reports whether cause, or anything in its caused_by chain, is a
// rejected execution.
func isRejection(cause *ErrorCause) bool {
	for c := cause; c != nil; c = c.CausedBy {
		switch c.Type {
		case rejectedExecutionType, esRejectedExecutionType:
			return true
		}
	}
	return false
}

// waitCtx waits for d, or returns ctx's error if it is done first.
func waitCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// flattenHits yields every hit of every page.
func flattenHits(pages iter.Seq[*SearchResp]) iter.Seq[SearchHit] {
	return func(yield func(SearchHit) bool) {
		for resp := range pages {
			for _, h := range resp.Hits.Hits {
				if !yield(h) {
					return
				}
			}
		}
	}
}

// checkPagingReq checks a request for the search_after iterators before
// anything is sent.
func checkPagingReq(req *SearchReq) error {
	switch {
	case req == nil || req.Body == nil:
		return errors.New("opensearchapi: search_after paging needs a typed Body")
	case req.BodyReader != nil:
		return errors.New("opensearchapi: search_after paging must not set BodyReader; search_after goes in Body")
	case req.Body.PIT != nil && len(req.Indices) > 0:
		return errPITWithIndices
	case req.Body.Sort == nil:
		return errors.New("opensearchapi: search_after paging needs Body.Sort, ending in a unique tiebreaker")
	case req.Body.Size != nil && *req.Body.Size <= 0:
		return fmt.Errorf("opensearchapi: search_after paging needs a positive Body.Size, got %d", *req.Body.Size)
	}
	return nil
}

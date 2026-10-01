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
	"net/http"
	"sync"
	"time"
)

// defaultPITKeepAlive is the keep_alive [PointInTimeClient.Open] sends when the
// request leaves it unset. The server frees a PIT once keep_alive passes without
// a search against it, so this bounds how long a crashed caller leaks one, while
// leaving room for slow consumers and retry backoff between pages.
const defaultPITKeepAlive = 300 * time.Second

// oneShotCloseTimeout bounds the delete the one-shot iterators
// ([PointInTimeClient.SearchAfterPages] and [PointInTimeClient.SearchAfter])
// send for the PIT they created. Their caller has no way to pass a context for
// that close, and the request context may be done by then, so without a bound
// of its own a hung delete would hang the end of the loop. A PIT the delete
// misses is still freed when its keep_alive runs out.
const oneShotCloseTimeout = 10 * time.Second

// ErrPITClosed is returned by [PIT.Search] and the PIT iterators once the PIT
// has been closed. A request that fails validation reports that error instead,
// whether or not the PIT is closed.
var ErrPITClosed = errors.New("opensearchapi: PIT is closed")

// PIT is an open point-in-time created by [PointInTimeClient.Open]. It is safe
// for concurrent use and can be passed between callers. The owner closes it
// with [PIT.Close]; closing it while a search is in flight makes that search
// fail with the server's error.
//
// Every request and response field that carries a PIT ID has type [PITID]:
// [SearchPointInTimeReference].ID (a search body's "pit"), [DeletePITBody].PITID,
// [CreatePITResp].PITID, and [SearchResp].PITID among them. [PIT.Search] and
// [SearchCursor.Apply] set the search field, [PIT.Close] sends the delete, and
// [SearchResp.Cursor] reads the ID a search echoes, so callers rarely touch the
// fields. [PITID] is opaque: build one from a stored string with [ParsePITID]
// and read it back with [PITID.String]. The ID encodes index names and node
// IDs; wrap or encrypt it before handing it to clients outside your service.
// See [SearchCursor] to resume a scan in a later request.
type PIT struct {
	client *Client
	id     PITID

	mu struct {
		sync.Mutex
		closed bool // set once a delete succeeds or the server no longer has the PIT
	}
}

// Open creates a point-in-time over req.Indices and returns a handle to it.
// If req.Params is nil or its KeepAlive is zero, Open uses a keep_alive of 300 seconds.
// Unlike [IndicesClient.Open], which opens a closed index, Open creates a PIT.
func (c PointInTimeClient) Open(ctx context.Context, req *CreatePITReq) (*PIT, error) {
	var create CreatePITReq
	if req != nil {
		create = *req
	}
	var params CreatePITParams
	if create.Params != nil {
		params = *create.Params
	}
	if params.KeepAlive == 0 {
		params.KeepAlive = defaultPITKeepAlive
	}
	create.Params = &params

	resp, err := c.Create(ctx, &create)
	if err != nil {
		return nil, err
	}
	if !resp.PITID.IsSet() {
		return nil, errors.New("opensearchapi: create PIT response has no pit_id")
	}
	return &PIT{client: c.apiClient, id: resp.PITID}, nil
}

// ID returns the PIT ID. To resume a scan in a later request or another
// process, keep the [SearchCursor] from a page's [SearchResp.Cursor], which
// carries this ID, and pass it back with [SearchCursor.Apply].
func (p *PIT) ID() PITID { return p.id }

// Search runs req against the PIT. The server restarts the PIT's keep_alive,
// the one set on Open, on every search. req.Body must be set, and req.Indices
// must be empty because the PIT already pins the indices. req is not modified.
// Partial failures follow the client's error mask, as for [Client.Search].
func (p *PIT) Search(ctx context.Context, req *SearchReq) (*SearchResp, error) {
	if err := checkPITSearchReq(req); err != nil {
		return nil, err
	}
	if p.isClosed() {
		return nil, ErrPITClosed
	}
	search := *req
	body := *req.Body
	body.PIT = &SearchPointInTimeReference{ID: p.id}
	search.Body = &body
	return p.client.Search(ctx, &search)
}

// Close deletes the PIT using ctx; the client adds no timeout of its own. The
// context that searched the PIT is often done by the time it is closed, as in
// an error path or a deferred close, so pass one made for the cleanup:
//
//	defer func() {
//		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
//		defer cancel()
//		if err := pit.Close(closeCtx); err != nil {
//			log.Printf("close PIT: %v", err)
//		}
//	}()
//
// Once a delete succeeds, or the server reports the PIT is already gone, the
// PIT is closed and further closes return nil. After a failed delete, a later
// Close tries again.
func (p *PIT) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mu.closed {
		return nil
	}
	resp, err := p.client.PIT.Delete(ctx, &DeletePITReq{Body: &DeletePITBody{PITID: []PITID{p.id}}})
	if err == nil {
		// Checked here too because the client's error mask can hide it, and a
		// PIT the server kept must not count as closed.
		if failure := resp.PITDeleteItemFailures(); failure != nil {
			err = failure
		}
	}
	if err != nil && !isPITGone(resp) {
		return fmt.Errorf("opensearchapi: delete PIT: %w", err)
	}
	p.mu.closed = true
	return nil
}

func (p *PIT) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mu.closed
}

// isPITGone reports whether the server answered a delete with 404, meaning the
// PIT has already expired or been deleted. OpenSearch 3.8 answers a repeated
// delete with 200 and "successful": true instead; this covers a server that
// reports it as not found.
func isPITGone(resp *DeletePITResp) bool {
	return resp != nil && resp.Inspect().Response != nil && resp.Inspect().Response.StatusCode == http.StatusNotFound
}

// SearchAfterPages is [Client.SearchAfterPages] over the PIT: it pages req
// through the PIT's snapshot, so req.Indices must be empty. Each page goes
// through [PIT.Search], so a scan stops with [ErrPITClosed] once the PIT is
// closed. SearchAfterPages does not close the PIT.
func (p *PIT) SearchAfterPages(ctx context.Context, req *SearchReq) (iter.Seq[*SearchResp], func() error) {
	if err := checkPITPagingReq(req); err != nil {
		return func(func(*SearchResp) bool) {}, func() error { return err }
	}
	return searchAfterPages(ctx, req, p.Search, p.client.retryBackoff())
}

// SearchAfter is [PIT.SearchAfterPages] flattened to hits.
func (p *PIT) SearchAfter(ctx context.Context, req *SearchReq) (iter.Seq[SearchHit], func() error) {
	pages, errf := p.SearchAfterPages(ctx, req)
	return flattenHits(pages), errf
}

// SearchAfterPages opens a PIT with createReq, pages req over it as
// [PIT.SearchAfterPages] does, and closes the PIT when the loop ends, fails, or
// the caller breaks out early. The close uses ctx without its cancellation, so
// the PIT is still deleted when ctx is why the loop ended; it keeps ctx's values
// and waits at most 10 seconds. The returned func reports paging and close
// errors together. req is checked before the PIT is created.
func (c PointInTimeClient) SearchAfterPages(
	ctx context.Context, createReq *CreatePITReq, req *SearchReq,
) (iter.Seq[*SearchResp], func() error) {
	var err error
	seq := func(yield func(*SearchResp) bool) {
		if verr := checkPITPagingReq(req); verr != nil {
			err = verr
			return
		}
		pit, oerr := c.Open(ctx, createReq)
		if oerr != nil {
			err = oerr
			return
		}
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), oneShotCloseTimeout)
			defer cancel()
			err = errors.Join(err, pit.Close(closeCtx))
		}()

		pages, errf := searchAfterPages(ctx, req, pit.Search, c.apiClient.retryBackoff())
		for resp := range pages {
			if !yield(resp) {
				break
			}
		}
		err = errf()
	}
	return seq, func() error { return err }
}

// SearchAfter is [PointInTimeClient.SearchAfterPages] flattened to hits.
func (c PointInTimeClient) SearchAfter(
	ctx context.Context, createReq *CreatePITReq, req *SearchReq,
) (iter.Seq[SearchHit], func() error) {
	pages, errf := c.SearchAfterPages(ctx, createReq, req)
	return flattenHits(pages), errf
}

// errPITWithIndices rejects a PIT search that also names indices: the PIT
// already pins them, and the server refuses the combination.
var errPITWithIndices = errors.New("opensearchapi: a PIT search must not set Indices; the PIT pins them")

// checkPITSearchReq checks a request for [PIT.Search] before anything is sent.
func checkPITSearchReq(req *SearchReq) error {
	switch {
	case req == nil || req.Body == nil:
		return errors.New("opensearchapi: a PIT search needs a typed Body")
	case req.BodyReader != nil:
		return errors.New("opensearchapi: a PIT search must not set BodyReader; the PIT reference goes in Body")
	case len(req.Indices) > 0:
		return errPITWithIndices
	}
	return nil
}

// checkPITPagingReq checks a request for the PIT iterators before anything is
// sent: the paging checks, plus no Indices, which the handle's PIT pins even
// though req.Body.PIT is not set yet.
func checkPITPagingReq(req *SearchReq) error {
	if req != nil && len(req.Indices) > 0 {
		return errPITWithIndices
	}
	return checkPagingReq(req)
}

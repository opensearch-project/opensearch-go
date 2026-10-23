// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	opensearch "github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/internal/scheduler"
	"github.com/opensearch-project/opensearch-go/v5/internal/slicepool"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

const (
	// mgetPreferenceLocal makes the receiving node answer from its own copy of
	// each shard, and fall back to another copy when it holds none, so a request
	// sent to the wrong node still gets the right answer.
	mgetPreferenceLocal = "_local"

	// mgetDirectFailureType is the error type of a document the driver answers
	// itself, because no request for it, direct or through a coordinating node,
	// completed. The cluster answers a document whose shard request failed the
	// same way, with the failure's exception (TransportMultiGetAction).
	mgetDirectFailureType = "mget_direct_request_exception"

	mgetRetryBase = 50 * time.Millisecond
	mgetRetryMax  = time.Second
)

var (
	// errMGetNotSplittable is wrapped by every reason the direct driver leaves an
	// mget to the cluster as one request.
	errMGetNotSplittable = errors.New("mget cannot be split the way the cluster would split it")

	errMGetNoDocs        = fmt.Errorf("%w: it has no docs, a raw BodyReader, or ids in place of docs", errMGetNotSplittable)
	errMGetPreference    = fmt.Errorf("%w: it has a preference", errMGetNotSplittable)
	errMGetReshaped      = fmt.Errorf("%w: filter_path, format or source reshapes it", errMGetNotSplittable)
	errMGetNoneResolved  = fmt.Errorf("%w: no document resolves to a node", errMGetNotSplittable)
	errNoUsableShardCopy = errors.New("no copy of the shard is on a usable node")
)

// mgetRetryableTypes are the per-document error types that say the document's
// shard copy could not answer, rather than anything about the document, so
// asking again, perhaps another copy, can succeed.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var mgetRetryableTypes = map[string]struct{}{
	"no_shard_available_action_exception": {},
	"shard_not_found_exception":           {},
	"unavailable_shards_exception":        {},
	"illegal_index_shard_state_exception": {},
	"node_not_connected_exception":        {},
	"node_disconnected_exception":         {},
	"node_closed_exception":               {},
	"connect_transport_exception":         {},
	"receive_timeout_transport_exception": {},
}

// mgetRejectedTypes are the per-document error types that say the node was
// overloaded. They are retried, and they shrink the node's window as a 429
// does.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var mgetRejectedTypes = map[string]struct{}{
	"opensearch_rejected_execution_exception": {},
	"circuit_breaking_exception":              {},
}

// mgetSender sends one request to a node.
type mgetSender func(ctx context.Context, c *Client, node *opensearchtransport.Connection, req MGetReq) (*MGetResp, error)

// sendMGetToNode sends req to node, answered from the node's own shard copies,
// as an ordinary mget so the generated dispatch decodes it.
func sendMGetToNode(ctx context.Context, c *Client, node *opensearchtransport.Connection, req MGetReq) (*MGetResp, error) {
	ctx = opensearchtransport.WithReadNode(ctx, node)

	return c.Doc.MGet(WithMGetDriver(ctx, MGetDriverCoordinator), req)
}

// mgetDriver fetches an mget's documents from the nodes that hold them. One
// driver serves every call a client makes, so its node windows learn from all
// of them.
type mgetDriver struct {
	driver      MGetDriver
	lookup      shardLookup
	send        mgetSender
	saturated   func(*opensearchtransport.Connection) bool
	windows     *scheduler.NodeWindows
	observer    MGetObserver
	maxDocs     int
	maxAttempts int
}

//nolint:gochecknoglobals // A pool is process-wide by design.
var mgetTargetPool slicepool.Pool[opensearchtransport.ShardTarget]

// mgetBatch is documents bound for one node in one request.
type mgetBatch struct {
	node *opensearchtransport.Connection
	// local sends preference=_local. Without it the node routes each document
	// as the coordinator would, which sends a segment-replicated index's
	// realtime read to the primary.
	local bool
	// slots are the positions of the documents in the caller's request.
	slots   []int
	attempt int
}

// mgetCopy is the shard copy selectCopy chose for a document: the node to send
// it to, and whether the request carries preference=_local.
type mgetCopy struct {
	node  *opensearchtransport.Connection
	local bool
}

// mgetCall is one mget the direct driver is answering.
type mgetCall struct {
	d   *mgetDriver
	c   *Client
	req MGetReq
	// realtime is the request's realtime, true unless it says otherwise.
	realtime bool
	// routing is the request's default routing, applied to documents with none.
	routing string
	// indexes is the concrete index each document was looked up in, or "".
	indexes []string

	batches []mgetBatch
	// unresolved are the positions resolve could not put on a node, fetched
	// through a coordinating node.
	unresolved []int

	// docs are the answers, one per position. A position is written by the one
	// goroutine that holds it at that moment, so it needs no lock.
	docs []MGetRespItem

	// cancel stops every request of the call, to resend it whole.
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu struct {
		sync.Mutex
		header http.Header
		resend bool
		// err is the failure returned should no request succeed, and errResp
		// the response that came with it, if any; see recordErr.
		err      error
		errResp  *MGetResp
		answered int
		nodes    map[string]struct{}
		stats    MGetDoneEvent
	}
}

// write takes req's documents and resolves each to the node to send it to,
// ready for flush. It returns an error wrapping errMGetNotSplittable, saying
// why, when req is not one the client can split as the cluster would, or when no
// document resolves to a node; the mget then goes to the cluster as it is.
func (d *mgetDriver) write(c *Client, req MGetReq) (*mgetCall, error) {
	switch {
	case req.BodyReader != nil || req.Body == nil || req.Body.IDs != nil || len(req.Body.Docs) == 0:
		return nil, errMGetNoDocs
	case req.Params != nil && req.Params.Preference != "":
		return nil, errMGetPreference
	case req.Params != nil && mgetReshapesResponse(req.Params.DebugParams):
		return nil, errMGetReshaped
	}

	n := len(req.Body.Docs)
	call := &mgetCall{
		d:        d,
		c:        c,
		req:      req,
		realtime: req.Params == nil || req.Params.Realtime == nil || *req.Params.Realtime,
		indexes:  make([]string, n),
		docs:     make([]MGetRespItem, n),
	}
	if req.Params != nil {
		call.routing = strings.Join(req.Params.Routing, ",")
	}

	positions := make([]int, n)
	for i := range positions {
		positions[i] = i
	}
	call.batches, call.unresolved = call.resolve(positions, 1, "")
	if len(call.batches) == 0 {
		return nil, errMGetNoneResolved
	}

	return call, nil
}

// mgetReshapesResponse reports whether p changes the response's shape or the
// request's body, so that a node's answer can no longer be matched to the
// documents asked for: filter_path can drop each document's _id, format changes
// the encoding, and source replaces the body.
func mgetReshapesResponse(p DebugParams) bool {
	return len(p.FilterPath) > 0 || p.Format != "" || p.Source != ""
}

// resolve groups the documents at positions by the node to send each to, in
// batches of at most maxDocs, for the given attempt. Positions it cannot
// resolve to a node are returned separately, to be fetched through a
// coordinating node. avoid names a node to pass over when a document has
// another copy to go to.
func (call *mgetCall) resolve(positions []int, attempt int, avoid string) ([]mgetBatch, []int) {
	type groupKey struct {
		node  string
		local bool
	}

	var (
		unresolved []int
		order      []groupKey
		groups     = make(map[groupKey]*mgetBatch)
		planned    = make(map[string]int)
	)

	targets := mgetTargetPool.Get()
	defer mgetTargetPool.Put(targets)

	for _, pos := range positions {
		op := call.req.Body.Docs[pos]
		index := call.req.Index
		if op.Index != nil && *op.Index != "" {
			index = *op.Index
		}
		key := op.ID
		switch {
		case op.Routing != nil && *op.Routing != "":
			key = *op.Routing
		case call.routing != "":
			key = call.routing
		}

		if index == "" || op.ID == "" || call.d.lookup.LookupShard(index, key, targets) != nil {
			unresolved = append(unresolved, pos)

			continue
		}
		call.indexes[pos] = index

		shardCopy, err := call.selectCopy(*targets, avoid, planned)
		if err != nil {
			unresolved = append(unresolved, pos)

			continue
		}
		planned[shardCopy.node.Name]++

		gk := groupKey{node: shardCopy.node.Name, local: shardCopy.local}
		b, found := groups[gk]
		if !found {
			b = &mgetBatch{node: shardCopy.node, local: shardCopy.local, attempt: attempt}
			groups[gk] = b
			order = append(order, gk)
		}
		b.slots = append(b.slots, pos)
	}

	var batches []mgetBatch
	for _, gk := range order {
		b := groups[gk]
		for start := 0; start < len(b.slots); start += call.d.maxDocs {
			end := min(start+call.d.maxDocs, len(b.slots))
			batches = append(batches, mgetBatch{node: b.node, local: b.local, attempt: attempt, slots: b.slots[start:end:end]})
		}
	}

	return batches, unresolved
}

// selectCopy picks the shard copy a document is read from. A realtime read of
// an index not known to use document replication goes to the primary with no
// preference, so the node itself sends it to the primary as the cluster would.
// Otherwise it goes to the copy whose node has the fewest documents outstanding,
// counting those resolved in this pass, preferring a replica on a tie. It
// returns errNoUsableShardCopy when no copy is on a usable node.
func (call *mgetCall) selectCopy(
	targets []*opensearchtransport.ShardTarget, avoid string, planned map[string]int,
) (mgetCopy, error) {
	if len(targets) == 0 {
		return mgetCopy{}, errNoUsableShardCopy
	}

	// A replication type not known to be document replication is treated as
	// segment replication, whose replicas can trail the primary.
	if call.realtime && targets[0].Replication != opensearchtransport.ReplicationDocument {
		primary := targets[0]
		if !primary.Primary || primary.Node == nil {
			return mgetCopy{}, errNoUsableShardCopy
		}

		return mgetCopy{node: primary.Node}, nil
	}

	var (
		best     *opensearchtransport.ShardTarget
		bestLoad int64
	)
	for _, target := range targets {
		if target.Node == nil {
			continue
		}
		if target.Node.Name == avoid && len(targets) > 1 {
			continue
		}

		load := int64(planned[target.Node.Name])
		if st, ok := call.d.windows.Stats(scheduler.NodeName(target.Node.Name)); ok {
			load += st.InFlight
		}

		switch {
		case best == nil, load < bestLoad:
		case load == bestLoad && best.Primary && !target.Primary:
		case load == bestLoad && best.Primary == target.Primary && target.Node.Name < best.Node.Name:
		default:
			continue
		}
		best, bestLoad = target, load
	}
	if best == nil {
		return mgetCopy{}, errNoUsableShardCopy
	}

	return mgetCopy{node: best.Node, local: true}, nil
}

// flush sends the written requests, waits for them, and returns the answer.
func (call *mgetCall) flush(parent context.Context) (*MGetResp, error) {
	start := time.Now()
	ctx, cancel := context.WithCancel(parent)
	call.cancel = cancel
	defer cancel()

	call.mu.nodes = make(map[string]struct{})
	call.mu.stats.Docs = len(call.docs)

	if len(call.unresolved) > 0 {
		call.coordinate(ctx, call.unresolved)
	}
	for _, b := range call.batches {
		call.dispatch(ctx, b)
	}
	call.wg.Wait()

	resp, err := call.finish(parent)

	call.mu.Lock()
	stats := call.mu.stats
	stats.Nodes = len(call.mu.nodes)
	call.mu.Unlock()
	stats.Duration = time.Since(start)
	stats.Err = err
	if call.d.observer != nil {
		call.d.observer.OnMGetDone(parent, stats)
	}

	return resp, err
}

// finish turns the answers into the call's result.
func (call *mgetCall) finish(parent context.Context) (*MGetResp, error) {
	if err := parent.Err(); err != nil {
		return &MGetResp{}, err
	}

	call.mu.Lock()
	resend, answered, err, errResp, header := call.mu.resend, call.mu.answered, call.mu.err, call.mu.errResp, call.mu.header
	call.mu.Unlock()

	if resend {
		call.mu.Lock()
		call.mu.stats.Resent = true
		call.mu.stats.Requests++
		call.mu.Unlock()

		return call.c.Doc.MGet(WithMGetDriver(parent, MGetDriverCoordinator), call.req)
	}
	if answered == 0 && err != nil {
		// No node answered at all, as the coordinator path would have failed.
		if errResp == nil {
			errResp = &MGetResp{}
		}

		return errResp, err
	}

	return call.merge(header), nil
}

// dispatch sends a batch on its own goroutine.
func (call *mgetCall) dispatch(ctx context.Context, b mgetBatch) {
	call.wg.Go(func() { call.sendBatch(ctx, b) })
}

// coordinate fetches the documents at slots through a coordinating node, on its
// own goroutine.
func (call *mgetCall) coordinate(ctx context.Context, slots []int) {
	call.mu.Lock()
	call.mu.stats.CoordinatorDocs += len(slots)
	call.mu.Unlock()

	call.wg.Go(func() { call.sendCoordinated(ctx, slots) })
}

// subRequest builds the request for the documents at slots, with the caller's
// parameters and the given preference.
func (call *mgetCall) subRequest(slots []int, preference string) MGetReq {
	ops := make([]MGetOperation, len(slots))
	for i, pos := range slots {
		ops[i] = call.req.Body.Docs[pos]
	}

	var params MGetParams
	if call.req.Params != nil {
		params = *call.req.Params
	}
	params.Preference = preference

	return MGetReq{Index: call.req.Index, Body: &MGetBody{Docs: ops}, Header: call.req.Header, Params: &params}
}

// sendBatch sends one batch to its node and deals with the answer.
func (call *mgetCall) sendBatch(ctx context.Context, b mgetBatch) {
	preference := ""
	if b.local {
		preference = mgetPreferenceLocal
	}
	sub := call.subRequest(b.slots, preference)

	reservation, err := call.d.windows.Acquire(ctx, scheduler.NodeName(b.node.Name), int64(len(b.slots)))
	if err != nil {
		return
	}

	call.mu.Lock()
	call.mu.nodes[b.node.Name] = struct{}{}
	call.mu.stats.Requests++
	if b.attempt > 1 {
		call.mu.stats.Retries++
	}
	call.mu.Unlock()

	sent := time.Now()
	resp, err := call.d.send(ctx, call.c, b.node, sub)
	event := MGetRequestEvent{
		Node:       b.node.Name,
		Attempt:    b.attempt,
		Docs:       len(b.slots),
		Duration:   time.Since(sent),
		StatusCode: mgetStatus(resp),
	}

	outcome, retry, fallback := call.answer(ctx, b, resp, err, &event)
	call.d.windows.Release(reservation, outcome, call.d.saturated(b.node))

	event.Err = err
	event.RetryDocs = len(retry) + len(fallback)
	if call.d.observer != nil {
		call.d.observer.OnMGetRequest(ctx, event)
	}

	if len(fallback) > 0 {
		call.coordinate(ctx, fallback)
	}
	if len(retry) == 0 {
		return
	}
	if b.attempt >= call.d.maxAttempts {
		call.coordinate(ctx, retry)

		return
	}
	if !mgetWait(ctx, b.attempt) {
		return
	}

	batches, unresolved := call.resolve(retry, b.attempt+1, b.node.Name)
	if len(unresolved) > 0 {
		call.coordinate(ctx, unresolved)
	}
	for _, next := range batches {
		call.dispatch(ctx, next)
	}
}

// answer places what a node's answer settles, and returns how the node fared,
// the documents to ask the nodes for again, and those to ask a coordinating node
// for.
func (call *mgetCall) answer(
	ctx context.Context, b mgetBatch, resp *MGetResp, err error, event *MGetRequestEvent,
) (scheduler.Outcome, []int, []int) {
	if err != nil {
		switch status := mgetStatus(resp); {
		case ctx.Err() != nil:
			return scheduler.OutcomeFailed, nil, nil
		case status == http.StatusTooManyRequests:
			event.Rejected = true

			return scheduler.OutcomeRejected, b.slots, nil
		case status == 0 || status >= http.StatusInternalServerError:
			call.recordErr(resp, err)

			return scheduler.OutcomeFailed, b.slots, nil
		default:
			// The cluster would refuse the whole mget the same way. Ask it for the
			// whole mget, and return what it says.
			call.resendWhole()

			return scheduler.OutcomeFailed, nil, nil
		}
	}

	if !mgetAnswerMatches(resp, call.req.Body.Docs, b.slots) {
		return scheduler.OutcomeFailed, nil, b.slots
	}

	outcome := scheduler.OutcomeAccepted
	var retry []int
	for i, pos := range b.slots {
		item := resp.Docs[i]
		if failure, ferr := item.MultiGetError(); ferr == nil {
			_, rejected := mgetRejectedTypes[failure.Error.Type]
			_, retryable := mgetRetryableTypes[failure.Error.Type]
			switch {
			case rejected:
				outcome = scheduler.OutcomeRejected
				event.Rejected = true
				retry = append(retry, pos)

				continue
			case retryable:
				retry = append(retry, pos)

				continue
			}
		}
		call.docs[pos] = item
	}
	call.answered(resp, len(b.slots)-len(retry))

	return outcome, retry, nil
}

// sendCoordinated asks a coordinating node for the documents at slots, and
// places its answer.
func (call *mgetCall) sendCoordinated(ctx context.Context, slots []int) {
	sub := call.subRequest(slots, "")

	call.mu.Lock()
	call.mu.stats.Requests++
	call.mu.Unlock()

	sent := time.Now()
	resp, err := call.c.Doc.MGet(WithMGetDriver(ctx, MGetDriverCoordinator), sub)
	event := MGetRequestEvent{Docs: len(slots), Duration: time.Since(sent), StatusCode: mgetStatus(resp), Err: err}
	if call.d.observer != nil {
		defer call.d.observer.OnMGetRequest(ctx, event)
	}

	switch status := mgetStatus(resp); {
	case ctx.Err() != nil:
		return
	case err != nil && status != 0 && status != http.StatusTooManyRequests && status < http.StatusInternalServerError:
		call.resendWhole()

		return
	case err != nil:
		call.recordErr(resp, err)
		call.fail(slots, err)

		return
	case !mgetAnswerMatches(resp, call.req.Body.Docs, slots):
		// The cluster answered, but not in a shape the documents can be placed
		// from. Ask it for the whole mget, and return what it says.
		call.resendWhole()

		return
	}

	for i, pos := range slots {
		call.docs[pos] = resp.Docs[i]
	}
	call.answered(resp, len(slots))
}

// mgetWait waits out the backoff before attempt+1, and reports false when ctx
// ends first.
func mgetWait(ctx context.Context, attempt int) bool {
	ceiling := min(mgetRetryBase<<(attempt-1), mgetRetryMax)
	delay := time.Duration(rand.Int64N(int64(ceiling) + 1)) //nolint:gosec // Jitter, not security.

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// answered records that a node's answer settled n documents.
func (call *mgetCall) answered(resp *MGetResp, n int) {
	call.mu.Lock()
	defer call.mu.Unlock()

	call.mu.answered += n
	if call.mu.header == nil && resp != nil && resp.response != nil {
		call.mu.header = resp.response.Header.Clone()
	}
}

// recordErr records a request's failure, to be the call's result should no
// request succeed. A failure with a response replaces one without, so the
// caller can inspect what the cluster said, as a failed coordinator request
// lets it.
func (call *mgetCall) recordErr(resp *MGetResp, err error) {
	call.mu.Lock()
	defer call.mu.Unlock()

	if call.mu.err == nil || (mgetStatus(call.mu.errResp) == 0 && mgetStatus(resp) != 0) {
		call.mu.err, call.mu.errResp = err, resp
	}
}

// resendWhole stops the call, to be answered by the cluster as one request.
func (call *mgetCall) resendWhole() {
	call.mu.Lock()
	call.mu.resend = true
	call.mu.Unlock()

	call.cancel()
}

// fail answers the documents at slots with err, as the cluster answers a
// document whose shard request failed.
func (call *mgetCall) fail(slots []int, err error) {
	call.mu.Lock()
	call.mu.stats.FailedDocs += len(slots)
	call.mu.Unlock()

	for _, pos := range slots {
		op := call.req.Body.Docs[pos]
		index := call.indexes[pos]
		switch {
		case index != "":
		case op.Index != nil:
			index = *op.Index
		default:
			index = call.req.Index
		}
		call.docs[pos] = mgetFailureItem(index, op.ID, err)
	}
}

// mgetFailureItem is the answer for a document whose every request failed, in
// the shape the cluster gives a failed document.
func mgetFailureItem(index, id string, err error) MGetRespItem {
	reason := err.Error()
	cause := ErrorCause{Type: mgetDirectFailureType, Reason: &reason}
	failure := MGetMultiGetError{
		ID:    id,
		Index: index,
		Error: ErrorCause{Type: mgetDirectFailureType, Reason: &reason, RootCause: []ErrorCause{cause}},
	}

	item := NewMGetRespItemFromMultiGetError(failure)
	if raw, merr := json.Marshal(failure); merr == nil {
		item.raw = raw
	}

	return item
}

// merge builds the response from the answers, already decoded from each node's
// response: the documents in the order they were asked for, and one raw body
// holding each document's answer as its node sent it, which each document's
// RawJSON then points into.
func (call *mgetCall) merge(header http.Header) *MGetResp {
	size := len(`{"docs":[]}`)
	for i := range call.docs {
		size += len(call.docs[i].raw) + 1
	}

	var buf bytes.Buffer
	buf.Grow(size)
	buf.WriteString(`{"docs":[`)

	spans := make([][2]int, len(call.docs))
	for i := range call.docs {
		if i > 0 {
			buf.WriteByte(',')
		}
		if len(call.docs[i].raw) == 0 {
			// A position nothing answered: every path places or fails each one,
			// so this is a defect, answered as a failed document rather than a
			// malformed body.
			call.docs[i] = mgetFailureItem(call.indexes[i], call.req.Body.Docs[i].ID, errors.New("mget: document was not answered"))
		}
		spans[i][0] = buf.Len()
		buf.Write(call.docs[i].raw)
		spans[i][1] = buf.Len()
	}
	buf.WriteString(`]}`)

	body := buf.Bytes()
	for i := range call.docs {
		call.docs[i].raw = body[spans[i][0]:spans[i][1]:spans[i][1]]
	}

	return &MGetResp{Docs: call.docs, response: opensearch.NewBufferedResponse(http.StatusOK, body, header)}
}

// mgetStatus returns the HTTP status of the response behind resp, or 0 when the
// request produced none.
func mgetStatus(resp *MGetResp) int {
	if resp == nil || resp.response == nil {
		return 0
	}

	return resp.response.StatusCode
}

// mgetAnswerMatches reports whether resp answers the documents at slots, one
// for one and in order. A node answers each mget in the order it was asked, so
// a mismatch means the answer cannot be placed.
func mgetAnswerMatches(resp *MGetResp, ops []MGetOperation, slots []int) bool {
	if resp == nil || len(resp.Docs) != len(slots) {
		return false
	}
	for i, pos := range slots {
		if mgetItemID(&resp.Docs[i]) != ops[pos].ID {
			return false
		}
	}

	return true
}

func mgetItemID(item *MGetRespItem) string {
	if doc, err := item.GetResult(); err == nil {
		return doc.ID
	}
	if failure, err := item.MultiGetError(); err == nil {
		return failure.ID
	}

	return ""
}

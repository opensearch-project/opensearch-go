// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
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
	errMGetNoneRouted    = fmt.Errorf("%w: no document routes to a node", errMGetNotSplittable)
	errNoUsableShardNode = errors.New("no copy of the shard is on a usable node")
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

// mgetJob is one request to one node: the documents it asks the node for.
type mgetJob struct {
	node *opensearchtransport.Connection
	// local sends preference=_local, so the node answers from its own shard
	// copies. Without it the node routes each document as a coordinator would,
	// which sends a realtime read of a segment-replicated index to the primary.
	local bool
	// slots are the positions of the job's documents in the caller's request.
	slots []int
	// attempt is 1 for a document's first request, and counts up on each retry.
	attempt int
}

// shardNode is the node findShardNode picked for a document, among the nodes
// that hold a copy of its shard, and whether the request to it carries
// preference=_local.
type shardNode struct {
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
	// params holds the caller's parameters for every sub-request: [0] without a
	// preference and [1] with preference=_local. They are set once per call and
	// shared, read-only, by its sub-requests.
	params [2]MGetParams

	jobs []mgetJob
	// unrouted are the positions route could not send to a node. They are
	// fetched through a coordinating node.
	unrouted []int

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

// write takes req's documents and routes each to the node to send it to, ready
// for flush. When req is not one the client can split the way the cluster would,
// or no document routes to a node, it returns an error that wraps
// errMGetNotSplittable and says why, and the mget goes to the cluster as one
// request.
func (d *mgetDriver) write(c *Client, req MGetReq) (*mgetCall, error) {
	switch {
	case req.BodyReader != nil || req.Body == nil || req.Body.IDs != nil || len(req.Body.Docs) == 0:
		return nil, errMGetNoDocs
	case req.Params != nil && req.Params.Preference != "":
		return nil, errMGetPreference
	case req.Params != nil && mgetReshapesResponse(req.Params.DebugParams):
		return nil, errMGetReshaped
	}

	call := &mgetCall{
		d:        d,
		c:        c,
		req:      req,
		realtime: req.Params == nil || req.Params.Realtime == nil || *req.Params.Realtime,
		docs:     make([]MGetRespItem, len(req.Body.Docs)),
	}
	if req.Params != nil {
		call.routing = strings.Join(req.Params.Routing, ",")
		call.params[0] = *req.Params
	}
	call.params[0].Preference = ""
	call.params[1] = call.params[0]
	call.params[1].Preference = mgetPreferenceLocal

	call.jobs, call.unrouted = call.route(nil, 1, "")
	if len(call.jobs) == 0 {
		return nil, errMGetNoneRouted
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

// route groups the documents at positions by the node each is sent to, into jobs
// of at most maxDocs documents, for the given attempt. A nil positions routes
// every document of the request. It returns the jobs, and the positions it could
// not route to a node, which are fetched through a coordinating node instead.
// avoid names a node to skip when a document's shard has a copy on another node.
func (call *mgetCall) route(positions []int, attempt int, avoid string) ([]mgetJob, []int) {
	type groupKey struct {
		node  string
		local bool
	}
	// group is the documents bound for one node with one preference. start is
	// where its positions begin in the shared slots array, and n how many there are.
	type group struct {
		node     *opensearchtransport.Connection
		local    bool
		start, n int
	}

	count := len(positions)
	if positions == nil {
		count = len(call.req.Body.Docs)
	}
	at := func(i int) int {
		if positions == nil {
			return i
		}

		return positions[i]
	}

	var (
		unrouted []int
		groups   []group
		index    = make(map[groupKey]int)
		planned  = make(map[string]int)
		// assigned is the group of the i-th position, or -1 when it is not routed.
		// Each document's node depends on the documents routed before it, so the
		// groups' sizes are known only after the pass that assigns them.
		assigned = make([]int32, count)
	)

	targets := mgetTargetPool.Get()
	defer mgetTargetPool.Put(targets)

	for i := range count {
		pos := at(i)
		assigned[i] = -1

		op := call.req.Body.Docs[pos]
		key := op.ID
		switch {
		case op.Routing != nil && *op.Routing != "":
			key = *op.Routing
		case call.routing != "":
			key = call.routing
		}

		idx := call.indexOf(pos)
		if idx == "" || op.ID == "" || call.d.lookup.LookupShard(idx, key, targets) != nil {
			unrouted = append(unrouted, pos)

			continue
		}

		sn, err := call.findShardNode(*targets, avoid, planned)
		if err != nil {
			unrouted = append(unrouted, pos)

			continue
		}
		planned[sn.node.Name]++

		gk := groupKey{node: sn.node.Name, local: sn.local}
		g, found := index[gk]
		if !found {
			g = len(groups)
			index[gk] = g
			groups = append(groups, group{node: sn.node, local: sn.local})
		}
		groups[g].n++
		assigned[i] = int32(g) //nolint:gosec // There are no more groups than nodes.
	}

	// Lay every group's positions out in one array, and cut each into jobs of at
	// most maxDocs.
	routed, numJobs := 0, 0
	for g := range groups {
		groups[g].start = routed
		routed += groups[g].n
		numJobs += (groups[g].n + call.d.maxDocs - 1) / call.d.maxDocs
	}
	slots := make([]int, routed)
	filled := make([]int, len(groups))
	for i, g := range assigned {
		if g < 0 {
			continue
		}
		slots[groups[g].start+filled[g]] = at(i)
		filled[g]++
	}

	jobs := make([]mgetJob, 0, numJobs)
	for _, grp := range groups {
		mine := slots[grp.start : grp.start+grp.n]
		for start := 0; start < len(mine); start += call.d.maxDocs {
			end := min(start+call.d.maxDocs, len(mine))
			jobs = append(jobs, mgetJob{node: grp.node, local: grp.local, attempt: attempt, slots: mine[start:end:end]})
		}
	}

	return jobs, unrouted
}

// findShardNode picks, among the nodes that hold a copy of a document's shard,
// the node to read the document from.
//
// A realtime read of an index not known to use document replication goes to the
// primary's node with no preference, so the node itself sends the read to the
// primary, as a coordinator would. Any other read goes to the node with the
// fewest documents in flight, counting the documents already routed in this
// pass, and a replica wins a tie. It returns errNoUsableShardNode when no copy
// is on a usable node.
func (call *mgetCall) findShardNode(
	targets []*opensearchtransport.ShardTarget, avoid string, planned map[string]int,
) (shardNode, error) {
	if len(targets) == 0 {
		return shardNode{}, errNoUsableShardNode
	}

	// A replication type not known to be document replication is treated as
	// segment replication, whose replicas can trail the primary.
	if call.realtime && targets[0].Replication != opensearchtransport.ReplicationDocument {
		primary := targets[0]
		if !primary.Primary || primary.Node == nil {
			return shardNode{}, errNoUsableShardNode
		}

		return shardNode{node: primary.Node}, nil
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
		return shardNode{}, errNoUsableShardNode
	}

	return shardNode{node: best.Node, local: true}, nil
}

// flush sends the written requests, waits for them, and returns the answer.
func (call *mgetCall) flush(parent context.Context) (*MGetResp, error) {
	start := time.Now()
	ctx, cancel := context.WithCancel(parent)
	call.cancel = cancel
	defer cancel()

	call.mu.nodes = make(map[string]struct{})
	call.mu.stats.Docs = len(call.docs)

	if len(call.unrouted) > 0 {
		call.coordinate(ctx, call.unrouted)
	}
	for _, j := range call.jobs {
		call.dispatch(ctx, j)
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

// dispatch sends a job on its own goroutine.
func (call *mgetCall) dispatch(ctx context.Context, j mgetJob) {
	call.wg.Go(func() { call.sendJob(ctx, j) })
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
// parameters; local adds preference=_local. Its body is encoded into a pooled
// buffer, and the caller releases the returned body once the request has
// returned. An error is from encoding a document, which the generated request
// would have hit as well.
func (call *mgetCall) subRequest(slots []int, local bool) (MGetReq, *mgetBody, error) {
	body, err := encodeMGetBody(call.req.Body.Docs, slots)
	if err != nil {
		return MGetReq{}, nil, err
	}

	params := &call.params[0]
	if local {
		params = &call.params[1]
	}

	return MGetReq{Index: call.req.Index, BodyReader: body, Header: call.req.Header, Params: params}, body, nil
}

// sendJob sends a job to its node once the node's window has room, injects the
// node's response, and sends again any documents that need another request.
func (call *mgetCall) sendJob(ctx context.Context, j mgetJob) {
	reservation, err := call.d.windows.Acquire(ctx, scheduler.NodeName(j.node.Name), int64(len(j.slots)))
	if err != nil {
		return
	}

	// Encoded once admitted, so that a job waiting for room holds no buffer.
	sub, body, err := call.subRequest(j.slots, j.local)
	if err != nil {
		call.d.windows.Release(reservation, scheduler.OutcomeUnknown, false)
		// The cluster's answer to the whole mget, encoded as the generated
		// request encodes it, is what the caller would have got.
		call.resendWhole()

		return
	}

	call.mu.Lock()
	call.mu.nodes[j.node.Name] = struct{}{}
	call.mu.stats.Requests++
	if j.attempt > 1 {
		call.mu.stats.Retries++
	}
	call.mu.Unlock()

	sent := time.Now()
	resp, err := call.d.send(ctx, call.c, j.node, sub)
	body.release()
	event := MGetRequestEvent{
		Node:       j.node.Name,
		Attempt:    j.attempt,
		Docs:       len(j.slots),
		Duration:   time.Since(sent),
		StatusCode: mgetStatus(resp),
	}

	outcome, retry, fallback := call.injectResponse(ctx, j, resp, err, &event)
	call.d.windows.Release(reservation, outcome, call.d.saturated(j.node))

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
	if j.attempt >= call.d.maxAttempts {
		call.coordinate(ctx, retry)

		return
	}
	if !mgetWait(ctx, j.attempt) {
		return
	}

	jobs, unrouted := call.route(retry, j.attempt+1, j.node.Name)
	if len(unrouted) > 0 {
		call.coordinate(ctx, unrouted)
	}
	for _, next := range jobs {
		call.dispatch(ctx, next)
	}
}

// injectResponse copies each document's answer from a node's response into
// that document's slot in the merged mget response. It returns the outcome to
// record in the node's window, the documents to send to a node again, and the
// documents to send to a coordinating node instead.
//
// A document whose answer says its shard copy could not respond is left out and
// returned for another request. A response that cannot be matched to the job's
// documents injects nothing and sends them all to a coordinating node.
func (call *mgetCall) injectResponse(
	ctx context.Context, j mgetJob, resp *MGetResp, err error, event *MGetRequestEvent,
) (scheduler.Outcome, []int, []int) {
	if err != nil {
		switch status := mgetStatus(resp); {
		case ctx.Err() != nil:
			return scheduler.OutcomeFailed, nil, nil
		case status == http.StatusTooManyRequests:
			event.Rejected = true

			return scheduler.OutcomeRejected, j.slots, nil
		case status == 0 || status >= http.StatusInternalServerError:
			call.recordErr(resp, err)

			return scheduler.OutcomeFailed, j.slots, nil
		default:
			// The cluster would refuse the whole mget the same way. Ask it for the
			// whole mget, and return what it says.
			call.resendWhole()

			return scheduler.OutcomeFailed, nil, nil
		}
	}

	if !mgetAnswerMatches(resp, call.req.Body.Docs, j.slots) {
		return scheduler.OutcomeFailed, nil, j.slots
	}

	outcome := scheduler.OutcomeAccepted
	var retry []int
	for i, pos := range j.slots {
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
	call.answered(resp, len(j.slots)-len(retry))

	return outcome, retry, nil
}

// sendCoordinated asks a coordinating node for the documents at slots, and
// places its answer.
func (call *mgetCall) sendCoordinated(ctx context.Context, slots []int) {
	sub, body, err := call.subRequest(slots, false)
	if err != nil {
		call.resendWhole()

		return
	}

	call.mu.Lock()
	call.mu.stats.Requests++
	call.mu.Unlock()

	sent := time.Now()
	resp, err := call.c.Doc.MGet(WithMGetDriver(ctx, MGetDriverCoordinator), sub)
	body.release()
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
		// The sub-response is not used again, so its header can be shared.
		call.mu.header = resp.response.Header
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
		call.docs[pos] = mgetFailureItem(call.indexOf(pos), call.req.Body.Docs[pos].ID, err)
	}
}

// indexOf returns the index the document at pos names: its own _index, or else
// the request's.
func (call *mgetCall) indexOf(pos int) string {
	if op := call.req.Body.Docs[pos]; op.Index != nil && *op.Index != "" {
		return *op.Index
	}

	return call.req.Index
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
//
// merge joins bytes rather than encoding them, and that is safe only because
// every byte it appends is one of three things:
//
//   - the constants {"docs":[ , and ]};
//   - a document's raw JSON from a node's response. The generated decoder runs
//     json.Unmarshal over the whole response, which validates all of it before
//     decoding, and hands each document exactly one complete JSON value from it;
//   - a failure item that mgetFailureItem built with json.Marshal.
//
// A complete JSON value cannot end the array early, so the result is valid
// JSON. Keep it so: anything else appended here must come from json.Marshal or
// from a buffer json.Unmarshal has validated. encoding/json's own Marshal is not
// used because it compacts each json.RawMessage and escapes <, > and & in it,
// which would change the bytes the cluster sent. TestMGetDirectMergeKeepsNodeBytes
// checks the invariant with hostile document content.
func (call *mgetCall) merge(header http.Header) *MGetResp {
	const open, closing = `{"docs":[`, `]}`

	size := len(open) + len(closing)
	for i := range call.docs {
		if len(call.docs[i].raw) == 0 {
			// A position nothing answered: every path places or fails each one,
			// so this is a defect, answered as a failed document rather than a
			// malformed body.
			call.docs[i] = mgetFailureItem(call.indexOf(i), call.req.Body.Docs[i].ID, errors.New("mget: document was not answered"))
		}
		size += len(call.docs[i].raw) + 1
	}

	body := make([]byte, 0, size)
	body = append(body, open...)
	for i := range call.docs {
		if i > 0 {
			body = append(body, ',')
		}
		body = append(body, call.docs[i].raw...)
	}
	body = append(body, closing...)

	// Point each document's raw JSON into body, from the lengths just written.
	off := len(open)
	for i := range call.docs {
		end := off + len(call.docs[i].raw)
		call.docs[i].raw = body[off:end:end]
		off = end + 1
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

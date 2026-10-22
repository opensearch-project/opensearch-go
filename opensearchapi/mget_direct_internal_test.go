// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	opensearch "github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// mgetFakeIndex is an index of the fake cluster: shard s of a document whose key
// ends in digit k is k mod shards, and its copies are on copies[s], primary first.
type mgetFakeIndex struct {
	shards int
	seg    bool
	copies [][]string
}

// mgetFake is a cluster the direct engine talks to without a transport: a shard
// map, nodes that answer from the shards they hold, and a coordinating node
// behind a real HTTP server.
type mgetFake struct {
	t       *testing.T
	indexes map[string]mgetFakeIndex
	conns   map[string]*opensearchtransport.Connection

	// node answers a request sent to a node; nil answers every document found.
	node func(node string, attempt int, req MGetReq) (status int, docs []string)
	// coord answers a request sent through the coordinating node; nil answers
	// every document found.
	coord func(req MGetBody) (status int, docs []string)

	mu struct {
		sync.Mutex
		sent      []mgetFakeSent
		coordReqs []MGetBody
		attempts  map[string]int
	}
}

// mgetFakeSent records one request to a node.
type mgetFakeSent struct {
	node       string
	preference string
	ids        []string
}

func newMGetFake(t *testing.T) *mgetFake {
	t.Helper()

	f := &mgetFake{
		t: t,
		indexes: map[string]mgetFakeIndex{
			"idx": {shards: 3, copies: [][]string{{"n0", "n1"}, {"n1", "n2"}, {"n2", "n0"}}},
			"seg": {shards: 3, seg: true, copies: [][]string{{"n0", "n1"}, {"n1", "n2"}, {"n2", "n0"}}},
		},
		conns: map[string]*opensearchtransport.Connection{
			"n0": {Name: "n0"}, "n1": {Name: "n1"}, "n2": {Name: "n2"},
		},
	}
	f.mu.attempts = make(map[string]int)

	return f
}

func (f *mgetFake) shardOf(index, key string) int {
	k := int(key[len(key)-1] - '0')

	return k % f.indexes[index].shards
}

// LookupShard implements shardLookup.
func (f *mgetFake) LookupShard(index, key string, dst *[]*opensearchtransport.ShardTarget) error {
	*dst = (*dst)[:0]

	idx, ok := f.indexes[index]
	if !ok {
		return opensearchtransport.ErrShardMapUnknown
	}

	shard := f.shardOf(index, key)
	for i, name := range idx.copies[shard] {
		*dst = append(*dst, &opensearchtransport.ShardTarget{
			Shard:              shard,
			Node:               f.conns[name],
			Primary:            i == 0,
			SegmentReplication: idx.seg,
			Generation:         1,
		})
	}

	return nil
}

func mgetFound(index, id, via string) string {
	return fmt.Sprintf(`{"_index":%q,"_id":%q,"_version":1,"found":true,"_source":{"via":%q}}`, index, id, via)
}

func mgetFailed(index, id, typ string) string {
	return fmt.Sprintf(`{"_index":%q,"_id":%q,"error":{"root_cause":[{"type":%q,"reason":"r"}],"type":%q,"reason":"r"}}`,
		index, id, typ, typ)
}

func mgetResponse(t *testing.T, status int, docs []string) (*MGetResp, error) {
	t.Helper()

	body := []byte(`{"docs":[` + strings.Join(docs, ",") + `]}`)
	if status != http.StatusOK {
		body = []byte(`{"error":{"type":"x","reason":"x"},"status":` + strconv.Itoa(status) + `}`)
	}
	resp := &MGetResp{response: opensearch.NewBufferedResponse(status, body, http.Header{"X-Fake": {"1"}})}
	if status != http.StatusOK {
		return resp, fmt.Errorf("status: %d", status)
	}
	require.NoError(t, json.Unmarshal(body, resp))

	return resp, nil
}

func (f *mgetFake) indexOf(req MGetReq, op MGetOperation) string {
	if op.Index != nil {
		return *op.Index
	}

	return req.Index
}

// send implements mgetSender for the nodes of the fake cluster. A node answers
// only documents whose shard it holds a copy of.
func (f *mgetFake) send(_ context.Context, _ *Client, node *opensearchtransport.Connection, req MGetReq) (*MGetResp, error) {
	ids := make([]string, len(req.Body.Docs))
	for i, op := range req.Body.Docs {
		ids[i] = op.ID
		index := f.indexOf(req, op)
		key := op.ID
		if op.Routing != nil {
			key = *op.Routing
		}
		if !slices.Contains(f.indexes[index].copies[f.shardOf(index, key)], node.Name) {
			// The engine's goroutines send, so report without FailNow.
			f.t.Errorf("document %s/%s sent to %s, which holds no copy of its shard", index, op.ID, node.Name)
		}
	}

	f.mu.Lock()
	f.mu.sent = append(f.mu.sent, mgetFakeSent{node: node.Name, preference: req.Params.Preference, ids: ids})
	f.mu.attempts[node.Name]++
	attempt := f.mu.attempts[node.Name]
	f.mu.Unlock()

	if f.node != nil {
		if status, docs := f.node(node.Name, attempt, req); status != 0 {
			if status == -1 {
				return nil, errors.New("connection refused")
			}

			return mgetResponse(f.t, status, docs)
		}
	}

	docs := make([]string, len(req.Body.Docs))
	for i, op := range req.Body.Docs {
		docs[i] = mgetFound(f.indexOf(req, op), op.ID, node.Name)
	}

	return mgetResponse(f.t, http.StatusOK, docs)
}

// serveCoordinator answers mgets sent through the coordinating node.
func (f *mgetFake) serveCoordinator(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/_mget") {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))

		return
	}

	var body MGetBody
	raw, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		f.t.Errorf("coordinator got a body it cannot decode: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}
	index := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/_mget")

	f.mu.Lock()
	f.mu.coordReqs = append(f.mu.coordReqs, body)
	f.mu.Unlock()

	status, docs := 0, []string(nil)
	if f.coord != nil {
		status, docs = f.coord(body)
	}
	if status == 0 {
		status = http.StatusOK
		for _, op := range body.Docs {
			i := index
			if op.Index != nil {
				i = *op.Index
			}
			docs = append(docs, mgetFound(i, op.ID, "coordinator"))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status != http.StatusOK {
		_, _ = fmt.Fprintf(w, `{"error":{"type":"x","reason":"x"},"status":%d}`, status)

		return
	}
	_, _ = w.Write([]byte(`{"docs":[` + strings.Join(docs, ",") + `]}`))
}

// client returns a client whose MGet uses the direct engine over the fake.
func (f *mgetFake) client(cfg MGetConfig) *Client {
	f.t.Helper()

	ts := httptest.NewServer(http.HandlerFunc(f.serveCoordinator))
	f.t.Cleanup(ts.Close)

	c, err := NewClient(Config{Client: opensearch.Config{Addresses: []string{ts.URL}}})
	require.NoError(f.t, err)
	f.t.Cleanup(func() { _ = c.Close() })

	cfg.Engine = MGetEngineDirect
	e := newMGetEngine(cfg, nil)
	e.engine = MGetEngineDirect
	e.lookup = f
	e.send = f.send
	e.saturated = func(*opensearchtransport.Connection) bool { return false }
	c.mget = e

	return c
}

func (f *mgetFake) sent() []mgetFakeSent {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.mu.sent)
}

func (f *mgetFake) coordinatorRequests() []MGetBody {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.mu.coordReqs)
}

func mgetDocs(index string, ids ...string) []MGetOperation {
	ops := make([]MGetOperation, len(ids))
	for i, id := range ids {
		ops[i] = MGetOperation{ID: id, Index: new(index)}
	}

	return ops
}

// requireAnswers checks that resp answers ops one for one, in order, and that
// its raw body is the same answers.
func requireAnswers(t *testing.T, ops []MGetOperation, resp *MGetResp) []json.RawMessage {
	t.Helper()

	require.Len(t, resp.Docs, len(ops))
	for i := range ops {
		require.Equal(t, ops[i].ID, mgetItemID(&resp.Docs[i]), "position %d", i)
	}

	require.Equal(t, http.StatusOK, resp.Inspect().Response.StatusCode)
	var raw struct {
		Docs []json.RawMessage `json:"docs"`
	}
	require.NoError(t, json.Unmarshal(resp.Inspect().Response.RawBody(), &raw))
	require.Len(t, raw.Docs, len(ops))
	for i := range raw.Docs {
		require.JSONEq(t, string(resp.Docs[i].RawJSON()), string(raw.Docs[i]), "position %d", i)
	}

	return raw.Docs
}

func via(t *testing.T, item MGetRespItem) string {
	t.Helper()

	doc, err := item.GetResult()
	require.NoError(t, err)
	var src struct {
		Via string `json:"via"`
	}
	require.NoError(t, json.Unmarshal(doc.Source, &src))

	return src.Via
}

func TestMGetDirectSplitsByNodeAndKeepsOrder(t *testing.T) {
	t.Parallel()

	f := newMGetFake(t)
	c := f.client(MGetConfig{})

	ops := mgetDocs("idx", "d0", "d1", "d2", "d3", "d4", "d5", "d6")
	resp, err := c.Doc.MGet(t.Context(), MGetReq{Body: &MGetBody{Docs: ops}})
	require.NoError(t, err)
	requireAnswers(t, ops, resp)

	sent := f.sent()
	require.NotEmpty(t, sent)
	nodes := map[string]bool{}
	var total int
	for _, s := range sent {
		require.Equal(t, mgetPreferenceLocal, s.preference, "a document-replicated read answers from the node's own copy")
		nodes[s.node] = true
		total += len(s.ids)
	}
	require.Len(t, nodes, 3, "the documents fan out over every node")
	require.Equal(t, len(ops), total, "each document is asked for once")
	require.Empty(t, f.coordinatorRequests(), "no coordinating node is needed")

	for i := range resp.Docs {
		require.NotEqual(t, "coordinator", via(t, resp.Docs[i]))
	}
}

func TestMGetDirectSegmentReplication(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		realtime *bool
		// wantPrimary is set when every document must go to its primary, with no
		// preference.
		wantPrimary bool
	}{
		{name: "a realtime read goes to the primary", wantPrimary: true},
		{name: "an explicit realtime read goes to the primary", realtime: new(true), wantPrimary: true},
		{name: "a near-realtime read may go to a replica", realtime: new(false)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newMGetFake(t)
			c := f.client(MGetConfig{})

			ops := mgetDocs("seg", "d0", "d1", "d2")
			_, err := c.Doc.MGet(t.Context(), MGetReq{Body: &MGetBody{Docs: ops}, Params: &MGetParams{Realtime: tt.realtime}})
			require.NoError(t, err)

			for _, s := range f.sent() {
				for _, id := range s.ids {
					primary := f.indexes["seg"].copies[f.shardOf("seg", id)][0]
					if tt.wantPrimary {
						require.Equal(t, primary, s.node, "document %s", id)
						require.Empty(t, s.preference, "the node routes to the primary itself")
					} else {
						require.Equal(t, mgetPreferenceLocal, s.preference)
					}
				}
			}
		})
	}
}

func TestMGetDirectSendsWhatItCannotPlaceThroughACoordinator(t *testing.T) {
	t.Parallel()

	f := newMGetFake(t)
	c := f.client(MGetConfig{})

	ops := []MGetOperation{
		{ID: "d0", Index: new("idx")},
		{ID: "u1", Index: new("alias-or-unknown")},
		{ID: "d2", Index: new("idx")},
		{ID: "u3"},
	}
	resp, err := c.Doc.MGet(t.Context(), MGetReq{Body: &MGetBody{Docs: ops}})
	require.NoError(t, err)
	requireAnswers(t, ops, resp)

	require.Equal(t, "coordinator", via(t, resp.Docs[1]))
	require.Equal(t, "coordinator", via(t, resp.Docs[3]))
	require.NotEqual(t, "coordinator", via(t, resp.Docs[0]))
	require.NotEqual(t, "coordinator", via(t, resp.Docs[2]))

	reqs := f.coordinatorRequests()
	require.Len(t, reqs, 1)
	ids := make([]string, len(reqs[0].Docs))
	for i, op := range reqs[0].Docs {
		ids[i] = op.ID
	}
	require.Equal(t, []string{"u1", "u3"}, ids, "only what the client cannot place goes to the coordinator")
}

func TestMGetDirectLeavesRequestsItCannotSplitToTheCluster(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  MGetReq
	}{
		{name: "a preference", req: MGetReq{Body: &MGetBody{Docs: mgetDocs("idx", "d0", "d1")}, Params: &MGetParams{Preference: "_primary"}}},
		{name: "a raw body", req: MGetReq{Index: "idx", BodyReader: strings.NewReader(`{"docs":[{"_id":"d0"}]}`)}},
		{name: "ids in place of docs", req: MGetReq{Index: "idx", Body: &MGetBody{IDs: new("d0")}}},
		{name: "nothing it can place", req: MGetReq{Body: &MGetBody{Docs: mgetDocs("unknown", "d0", "d1")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newMGetFake(t)
			f.coord = func(body MGetBody) (int, []string) {
				docs := make([]string, len(body.Docs))
				for i, op := range body.Docs {
					docs[i] = mgetFound("idx", op.ID, "coordinator")
				}

				return http.StatusOK, docs
			}
			c := f.client(MGetConfig{})

			_, err := c.Doc.MGet(t.Context(), tt.req)
			require.NoError(t, err)
			require.Empty(t, f.sent(), "no node is asked directly")
			require.Len(t, f.coordinatorRequests(), 1, "the mget goes to the cluster as it is")
		})
	}
}

func TestMGetDirectRetries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// fail answers a node's request; status 0 lets the node answer normally.
		fail func(node string, attempt int, req MGetReq) (int, []string)
		// wantCoordinator are the documents that end up answered through the
		// coordinating node.
		wantCoordinator []string
		wantRejected    bool
	}{
		{
			name: "a 429 is retried on another copy",
			fail: func(node string, attempt int, _ MGetReq) (int, []string) {
				if node == "n1" && attempt == 1 {
					return http.StatusTooManyRequests, nil
				}

				return 0, nil
			},
			wantRejected: true,
		},
		{
			name: "a node that cannot be reached is retried on another copy",
			fail: func(node string, _ int, _ MGetReq) (int, []string) {
				if node == "n1" {
					return -1, nil
				}

				return 0, nil
			},
		},
		{
			name: "a shard that stays unavailable is asked for through a coordinator",
			fail: func(_ string, _ int, req MGetReq) (int, []string) {
				docs := make([]string, len(req.Body.Docs))
				for i, op := range req.Body.Docs {
					if op.ID == "d1" {
						docs[i] = mgetFailed("idx", op.ID, "no_shard_available_action_exception")
					} else {
						docs[i] = mgetFound("idx", op.ID, "node")
					}
				}

				return http.StatusOK, docs
			},
			wantCoordinator: []string{"d1"},
		},
		{
			name: "a rejected execution is retried and counts as a rejection",
			fail: func(_ string, attempt int, req MGetReq) (int, []string) {
				if attempt > 1 {
					return 0, nil
				}
				docs := make([]string, len(req.Body.Docs))
				for i, op := range req.Body.Docs {
					docs[i] = mgetFailed("idx", op.ID, "opensearch_rejected_execution_exception")
				}

				return http.StatusOK, docs
			},
			wantRejected: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newMGetFake(t)
			f.node = tt.fail
			obs := &recordingMGetObserver{}
			c := f.client(MGetConfig{Observer: obs})

			ops := mgetDocs("idx", "d0", "d1", "d2", "d3")
			resp, err := c.Doc.MGet(t.Context(), MGetReq{Body: &MGetBody{Docs: ops}})
			require.NoError(t, err)
			requireAnswers(t, ops, resp)

			for i, op := range ops {
				_, gerr := resp.Docs[i].GetResult()
				require.NoError(t, gerr, "document %s is found in the end", op.ID)
				if slices.Contains(tt.wantCoordinator, op.ID) {
					require.Equal(t, "coordinator", via(t, resp.Docs[i]), "document %s", op.ID)
				}
			}

			done := obs.done()
			require.Len(t, done, 1)
			require.Positive(t, done[0].Retries+done[0].CoordinatorDocs, "something was asked for again")
			require.Equal(t, tt.wantRejected, obs.rejected(), "a rejection is reported")
		})
	}
}

func TestMGetDirectShrinksARejectingNodesWindow(t *testing.T) {
	t.Parallel()

	f := newMGetFake(t)
	f.node = func(node string, attempt int, _ MGetReq) (int, []string) {
		if node == "n1" && attempt == 1 {
			return http.StatusTooManyRequests, nil
		}

		return 0, nil
	}
	c := f.client(MGetConfig{MaxDocsPerRequest: 1, MaxInFlightDocsPerNode: 8})

	_, err := c.Doc.MGet(t.Context(), MGetReq{Body: &MGetBody{Docs: mgetDocs("idx", "d0", "d1", "d2")}})
	require.NoError(t, err)

	st, ok := c.mget.admission.State("n1")
	require.True(t, ok)
	require.Less(t, st.Fraction, 1.0, "the node that answered 429 has its window shrunk")
}

func TestMGetDirectPassesServerErrorsThrough(t *testing.T) {
	t.Parallel()

	f := newMGetFake(t)
	notFound := mgetFailed("idx", "d1", "index_not_found_exception")
	f.node = func(_ string, _ int, req MGetReq) (int, []string) {
		docs := make([]string, len(req.Body.Docs))
		for i, op := range req.Body.Docs {
			if op.ID == "d1" {
				docs[i] = notFound
			} else {
				docs[i] = mgetFound("idx", op.ID, "node")
			}
		}

		return http.StatusOK, docs
	}
	c := f.client(MGetConfig{})

	ops := mgetDocs("idx", "d0", "d1", "d2")
	resp, err := c.Doc.MGet(t.Context(), MGetReq{Body: &MGetBody{Docs: ops}})
	require.NoError(t, err)
	raw := requireAnswers(t, ops, resp)

	require.JSONEq(t, notFound, string(raw[1]), "the node's own answer is returned untouched")
	require.Empty(t, f.coordinatorRequests(), "an error that says nothing about the shard copy is not retried")
}

func TestMGetDirectResendsWhatTheClusterWouldRefuseWhole(t *testing.T) {
	t.Parallel()

	f := newMGetFake(t)
	f.node = func(string, int, MGetReq) (int, []string) { return http.StatusBadRequest, nil }
	obs := &recordingMGetObserver{}
	c := f.client(MGetConfig{Observer: obs})

	ops := mgetDocs("idx", "d0", "d1", "d2")
	resp, err := c.Doc.MGet(t.Context(), MGetReq{Body: &MGetBody{Docs: ops}})
	require.NoError(t, err)
	requireAnswers(t, ops, resp)

	reqs := f.coordinatorRequests()
	require.NotEmpty(t, reqs)
	require.Len(t, reqs[len(reqs)-1].Docs, len(ops), "the whole mget is sent through a coordinator")
	for i := range resp.Docs {
		require.Equal(t, "coordinator", via(t, resp.Docs[i]))
	}
	require.True(t, obs.done()[0].Resent)
}

func TestMGetDirectFailsDocumentsNoRequestCouldFetch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// down are the nodes that cannot be reached.
		down []string
		// wantErr is set when the call fails as a whole, because no node answered.
		wantErr bool
	}{
		{name: "a document whose copies are all down is answered with an error", down: []string{"n1", "n2"}},
		{name: "a call no node answers fails", down: []string{"n0", "n1", "n2"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newMGetFake(t)
			f.node = func(node string, _ int, _ MGetReq) (int, []string) {
				if slices.Contains(tt.down, node) {
					return -1, nil
				}

				return 0, nil
			}
			f.coord = func(MGetBody) (int, []string) { return http.StatusServiceUnavailable, nil }
			obs := &recordingMGetObserver{}
			c := f.client(MGetConfig{MaxAttempts: 2})

			// d1's shard 1 is on n1 and n2; d0's shard 0 is on n0 and n1.
			ops := mgetDocs("idx", "d0", "d1")
			c.mget.observer = obs
			resp, err := c.Doc.MGet(t.Context(), MGetReq{Body: &MGetBody{Docs: ops}})
			if tt.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
			requireAnswers(t, ops, resp)

			_, gerr := resp.Docs[0].GetResult()
			require.NoError(t, gerr, "d0 is fetched from n0")

			failure, ferr := resp.Docs[1].MultiGetError()
			require.NoError(t, ferr, "d1 is answered with an error")
			require.Equal(t, mgetDirectFailureType, failure.Error.Type)
			require.Equal(t, "idx", failure.Index)
			require.Equal(t, 1, obs.done()[0].FailedDocs)
		})
	}
}

func TestMGetDirectRefetchesAnAnswerItCannotPlace(t *testing.T) {
	t.Parallel()

	f := newMGetFake(t)
	f.node = func(_ string, _ int, req MGetReq) (int, []string) {
		return http.StatusOK, []string{mgetFound("idx", "wrong", "node")}[:min(1, len(req.Body.Docs))]
	}
	c := f.client(MGetConfig{})

	ops := mgetDocs("idx", "d0", "d1", "d2", "d3")
	resp, err := c.Doc.MGet(t.Context(), MGetReq{Body: &MGetBody{Docs: ops}})
	require.NoError(t, err)
	requireAnswers(t, ops, resp)
	for i := range resp.Docs {
		require.Equal(t, "coordinator", via(t, resp.Docs[i]))
	}
}

func TestMGetDirectObserverReportsTheFanOut(t *testing.T) {
	t.Parallel()

	f := newMGetFake(t)
	obs := &recordingMGetObserver{}
	c := f.client(MGetConfig{Observer: obs, MaxDocsPerRequest: 1})

	ops := mgetDocs("idx", "d0", "d1", "d2", "d3", "d4", "d5")
	_, err := c.Doc.MGet(t.Context(), MGetReq{Body: &MGetBody{Docs: ops}})
	require.NoError(t, err)

	done := obs.done()
	require.Len(t, done, 1)
	require.Equal(t, MGetDoneEvent{Docs: 6, Nodes: 3, Requests: 6}, MGetDoneEvent{
		Docs: done[0].Docs, Nodes: done[0].Nodes, Requests: done[0].Requests,
		Retries: done[0].Retries, CoordinatorDocs: done[0].CoordinatorDocs,
	})

	reqs := obs.requests()
	require.Len(t, reqs, 6)
	for _, r := range reqs {
		require.NotEmpty(t, r.Node)
		require.Equal(t, 1, r.Attempt)
		require.Equal(t, 1, r.Docs)
		require.Equal(t, http.StatusOK, r.StatusCode)
	}
}

func TestMGetDirectStopsWhenTheCallIsCanceled(t *testing.T) {
	t.Parallel()

	f := newMGetFake(t)
	ctx, cancel := context.WithCancel(t.Context())
	f.node = func(string, int, MGetReq) (int, []string) {
		cancel()

		return -1, nil
	}
	c := f.client(MGetConfig{})

	_, err := c.Doc.MGet(ctx, MGetReq{Body: &MGetBody{Docs: mgetDocs("idx", "d0", "d1")}})
	require.ErrorIs(t, err, context.Canceled)
}

func TestMGetEngineSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config MGetEngine
		ctx    MGetEngine
		want   MGetEngine
	}{
		{name: "the release default", want: defaultMGetEngine},
		{name: "the config", config: MGetEngineDirect, want: MGetEngineDirect},
		{name: "a context override", config: MGetEngineDirect, ctx: MGetEngineCoordinator, want: MGetEngineCoordinator},
		{name: "a default context keeps the config", config: MGetEngineDirect, ctx: MGetEngineDefault, want: MGetEngineDirect},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := newMGetEngine(MGetConfig{Engine: tt.config}, nil)
			ctx := t.Context()
			if tt.ctx != MGetEngineDefault {
				ctx = WithMGetEngine(ctx, tt.ctx)
			}
			require.Equal(t, tt.want, e.selected(ctx))
		})
	}
}

func TestParseMGetEngine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    MGetEngine
		wantErr bool
	}{
		{in: "", want: MGetEngineDefault},
		{in: "direct", want: MGetEngineDirect},
		{in: " Direct ", want: MGetEngineDirect},
		{in: "COORDINATOR", want: MGetEngineCoordinator},
		{in: "shard", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			got, err := ParseMGetEngine(tt.in)
			if tt.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			if tt.want != MGetEngineDefault {
				require.Equal(t, strings.ToLower(strings.TrimSpace(tt.in)), got.String())
			}
		})
	}
}

type recordingMGetObserver struct {
	mu   sync.Mutex
	reqs []MGetRequestEvent
	dn   []MGetDoneEvent
}

func (o *recordingMGetObserver) OnMGetRequest(_ context.Context, e MGetRequestEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reqs = append(o.reqs, e)
}

func (o *recordingMGetObserver) OnMGetDone(_ context.Context, e MGetDoneEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.dn = append(o.dn, e)
}

func (o *recordingMGetObserver) requests() []MGetRequestEvent {
	o.mu.Lock()
	defer o.mu.Unlock()

	return slices.Clone(o.reqs)
}

func (o *recordingMGetObserver) done() []MGetDoneEvent {
	o.mu.Lock()
	defer o.mu.Unlock()

	return slices.Clone(o.dn)
}

func (o *recordingMGetObserver) rejected() bool {
	for _, r := range o.requests() {
		if r.Rejected {
			return true
		}
	}

	return false
}

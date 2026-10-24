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
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	opensearch "github.com/opensearch-project/opensearch-go/v5"
	bufbody "github.com/opensearch-project/opensearch-go/v5/internal/body"
	"github.com/opensearch-project/opensearch-go/v5/internal/envvars"
	"github.com/opensearch-project/opensearch-go/v5/internal/slicepool"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// mgetFakeIndex is an index of the fake cluster: shard s of a document whose key
// ends in digit k is k mod shards, and its copies are on copies[s], primary first.
type mgetFakeIndex struct {
	shards      int
	replication opensearchtransport.ReplicationType
	copies      [][]string
}

// mgetFake is a cluster the direct driver talks to without a transport: a shard
// map, nodes that answer from the shards they hold, and a coordinating node
// behind a real HTTP server.
type mgetFake struct {
	t       testing.TB
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

func newMGetFake(tb testing.TB) *mgetFake {
	tb.Helper()

	copies := [][]string{{"n0", "n1"}, {"n1", "n2"}, {"n2", "n0"}}
	f := &mgetFake{
		t: tb,
		indexes: map[string]mgetFakeIndex{
			"idx": {shards: 3, replication: opensearchtransport.ReplicationDocument, copies: copies},
			"seg": {shards: 3, replication: opensearchtransport.ReplicationSegment, copies: copies},
			"unk": {shards: 3, replication: opensearchtransport.ReplicationUnknown, copies: copies},
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

// LookupShard implements shardLookup. Like the transport's, it reuses the
// ShardTargets already in dst.
func (f *mgetFake) LookupShard(index, key string, dst *[]*opensearchtransport.ShardTarget) error {
	*dst = (*dst)[:0]

	idx, ok := f.indexes[index]
	if !ok {
		return opensearchtransport.ErrShardMapUnknown
	}

	shard := f.shardOf(index, key)
	for i, name := range idx.copies[shard] {
		*slicepool.Append(dst) = opensearchtransport.ShardTarget{
			Shard:       shard,
			Node:        f.conns[name],
			Primary:     i == 0,
			Replication: idx.replication,
			Generation:  1,
		}
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

func mgetResponse(tb testing.TB, status int, docs []string) (*MGetResp, error) {
	tb.Helper()

	body := []byte(`{"docs":[` + strings.Join(docs, ",") + `]}`)
	if status != http.StatusOK {
		body = []byte(`{"error":{"type":"x","reason":"x"},"status":` + strconv.Itoa(status) + `}`)
	}
	resp := &MGetResp{response: opensearch.NewBufferedResponse(status, body, http.Header{"X-Fake": {"1"}})}
	if status != http.StatusOK {
		return resp, fmt.Errorf("status: %d", status)
	}
	require.NoError(tb, json.Unmarshal(body, resp))

	return resp, nil
}

func (f *mgetFake) indexOf(req MGetReq, op MGetOperation) string {
	if op.Index != nil {
		return *op.Index
	}

	return req.Index
}

// decodeBody returns req with the pooled body the driver sent decoded into
// req.Body, as a node would decode it.
func (f *mgetFake) decodeBody(req MGetReq) MGetReq {
	body, ok := req.BodyReader.(*bufbody.Pooled)
	if !ok {
		f.t.Errorf("the driver sent a %T body, not its pooled mget body", req.BodyReader)

		return req
	}

	var decoded MGetBody
	if err := json.Unmarshal(body.Bytes(), &decoded); err != nil {
		f.t.Errorf("the driver sent a body that is not an mget: %v: %s", err, body.Bytes())
	}
	req.Body = &decoded

	return req
}

// send implements mgetSender for the nodes of the fake cluster. A node answers
// only documents whose shard it holds a copy of.
func (f *mgetFake) send(_ context.Context, _ *Client, node *opensearchtransport.Connection, req MGetReq) (*MGetResp, error) {
	req = f.decodeBody(req)
	if body, ok := req.BodyReader.(*bufbody.Pooled); ok {
		// The transport closes a request body once it has sent it.
		defer body.Close()
	}

	ids := make([]string, len(req.Body.Docs))
	for i, op := range req.Body.Docs {
		ids[i] = op.ID
		index := f.indexOf(req, op)
		key := op.ID
		if op.Routing != nil {
			key = *op.Routing
		}
		if !slices.Contains(f.indexes[index].copies[f.shardOf(index, key)], node.Name) {
			// The driver's goroutines send, so report without FailNow.
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
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		// The driver canceled the request while sending it, as it does to every
		// request of an mget it resends whole; there is nothing to answer.
		return
	}
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

// client returns a client whose MGet uses the direct driver over the fake.
func (f *mgetFake) client(cfg MGetConfig) *Client {
	f.t.Helper()

	ts := httptest.NewServer(http.HandlerFunc(f.serveCoordinator))
	f.t.Cleanup(ts.Close)

	c, err := NewClient(Config{Client: opensearch.Config{Addresses: []string{ts.URL}}})
	require.NoError(f.t, err)
	f.t.Cleanup(func() { _ = c.Close() })

	cfg.Driver = MGetDriverDirect
	d := newMGetDriver(cfg, nil)
	d.driver = MGetDriverDirect
	d.lookup = f
	d.send = f.send
	d.saturated = func(*opensearchtransport.Connection) bool { return false }
	c.ext.mget = d

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

	// Every node of the fake holds copies of two of its three shards.
	tests := []struct {
		name      string
		fanOut    MGetFanOut
		wantNodes []string
	}{
		{
			name:   "fewest nodes covers the shards with two nodes, the first by name taking two shards",
			fanOut: MGetFanOutFewestNodes, wantNodes: []string{"n0", "n1"},
		},
		{name: "least loaded fans out over every node", fanOut: MGetFanOutLeastLoaded, wantNodes: []string{"n0", "n1", "n2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newMGetFake(t)
			c := f.client(MGetConfig{FanOut: tt.fanOut})

			ops := mgetDocs("idx", "d0", "d1", "d2", "d3", "d4", "d5", "d6")
			resp, err := c.Doc.MGet(t.Context(), MGetReq{Body: &MGetBody{Docs: ops}})
			require.NoError(t, err)
			requireAnswers(t, ops, resp)

			sent := f.sent()
			require.NotEmpty(t, sent)
			var nodes []string
			var total int
			for _, s := range sent {
				require.Equal(t, mgetPreferenceLocal, s.preference, "a document-replicated read answers from the node's own copy")
				if !slices.Contains(nodes, s.node) {
					nodes = append(nodes, s.node)
				}
				total += len(s.ids)
			}
			slices.Sort(nodes)
			require.Equal(t, tt.wantNodes, nodes)
			require.Len(t, sent, len(tt.wantNodes), "one request per node")
			require.Equal(t, len(ops), total, "each document is asked for once")
			require.Empty(t, f.coordinatorRequests(), "no coordinating node is needed")

			for i := range resp.Docs {
				require.NotEqual(t, "coordinator", via(t, resp.Docs[i]))
			}
		})
	}
}

func TestMGetDirectFewestNodesCover(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// saturated names the nodes whose get thread pool is rejecting work.
		saturated []string
		ops       []MGetOperation
		realtime  bool
		wantNodes []string
	}{
		{
			name:      "one shard is read from one node",
			ops:       mgetDocs("idx", "d0", "d3", "d6", "d9"),
			wantNodes: []string{"n0"},
		},
		{
			name:      "a saturated node is left out while its shards have other copies",
			saturated: []string{"n0"},
			ops:       mgetDocs("idx", "d0", "d1", "d2"),
			wantNodes: []string{"n1", "n2"},
		},
		{
			name:      "a shard whose every copy is saturated is still read",
			saturated: []string{"n0", "n1", "n2"},
			ops:       mgetDocs("idx", "d0", "d1", "d2"),
			wantNodes: []string{"n0", "n1"},
		},
		{
			name: "two indexes share the nodes they are read from",
			ops: append(mgetDocs("idx", "d0", "d1", "d2"),
				MGetOperation{ID: "e0", Index: new("seg")}, MGetOperation{ID: "e2", Index: new("seg")}),
			wantNodes: []string{"n0", "n1"},
		},
		{
			// Shard 1 of idx has copies on n1 and n2, and the tie would go to n1, but
			// the realtime read of seg's shard 2 already sends to its primary, n2.
			name:      "a node a primary read reaches takes the shards it holds",
			ops:       []MGetOperation{{ID: "d1", Index: new("idx")}, {ID: "e2", Index: new("seg")}},
			realtime:  true,
			wantNodes: []string{"n2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newMGetFake(t)
			c := f.client(MGetConfig{FanOut: MGetFanOutFewestNodes})
			c.ext.mget.saturated = func(conn *opensearchtransport.Connection) bool {
				return slices.Contains(tt.saturated, conn.Name)
			}

			resp, err := c.Doc.MGet(t.Context(), MGetReq{Body: &MGetBody{Docs: tt.ops}, Params: &MGetParams{Realtime: new(tt.realtime)}})
			require.NoError(t, err)
			requireAnswers(t, tt.ops, resp)

			var nodes []string
			for _, s := range f.sent() {
				if !slices.Contains(nodes, s.node) {
					nodes = append(nodes, s.node)
				}
			}
			slices.Sort(nodes)
			require.Equal(t, tt.wantNodes, nodes)
		})
	}
}

func TestMGetDirectReplicationType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		index    string
		realtime *bool
		// wantPrimary is set when every document must go to its primary, with no
		// preference.
		wantPrimary bool
	}{
		{name: "a realtime segment-replicated read goes to the primary", index: "seg", wantPrimary: true},
		{name: "an explicit realtime read goes to the primary", index: "seg", realtime: new(true), wantPrimary: true},
		{name: "a near-realtime segment-replicated read may go to a replica", index: "seg", realtime: new(false)},
		{name: "an unknown replication type is read as segment replication", index: "unk", wantPrimary: true},
		{name: "a realtime document-replicated read may go to a replica", index: "idx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newMGetFake(t)
			c := f.client(MGetConfig{})

			ops := mgetDocs(tt.index, "d0", "d1", "d2")
			_, err := c.Doc.MGet(t.Context(), MGetReq{Body: &MGetBody{Docs: ops}, Params: &MGetParams{Realtime: tt.realtime}})
			require.NoError(t, err)

			sent := f.sent()
			require.NotEmpty(t, sent)
			for _, s := range sent {
				for _, id := range s.ids {
					primary := f.indexes[tt.index].copies[f.shardOf(tt.index, id)][0]
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
		{
			name: "a filter_path",
			req: MGetReq{
				Body:   &MGetBody{Docs: mgetDocs("idx", "d0", "d1")},
				Params: &MGetParams{DebugParams: DebugParams{FilterPath: []string{"docs._source"}}},
			},
		},
		{
			name: "a source parameter",
			req: MGetReq{
				Body:   &MGetBody{Docs: mgetDocs("idx", "d0", "d1")},
				Params: &MGetParams{DebugParams: DebugParams{Source: `{"docs":[{"_index":"idx","_id":"d0"}]}`}},
			},
		},
		{
			name: "a format",
			req: MGetReq{
				Body:   &MGetBody{Docs: mgetDocs("idx", "d0", "d1")},
				Params: &MGetParams{DebugParams: DebugParams{Format: "yaml"}},
			},
		},
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

func TestMGetDriverWriteSaysWhyItCannotSplit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  MGetReq
		want error
	}{
		{name: "a raw body", req: MGetReq{Index: "idx", BodyReader: strings.NewReader(`{}`)}, want: errMGetNoDocs},
		{name: "ids in place of docs", req: MGetReq{Index: "idx", Body: &MGetBody{IDs: new("d0")}}, want: errMGetNoDocs},
		{
			name: "a preference",
			req:  MGetReq{Body: &MGetBody{Docs: mgetDocs("idx", "d0")}, Params: &MGetParams{Preference: "_primary"}},
			want: errMGetPreference,
		},
		{
			name: "a filter_path",
			req: MGetReq{
				Body:   &MGetBody{Docs: mgetDocs("idx", "d0")},
				Params: &MGetParams{DebugParams: DebugParams{FilterPath: []string{"docs._source"}}},
			},
			want: errMGetReshaped,
		},
		{name: "nothing routes", req: MGetReq{Body: &MGetBody{Docs: mgetDocs("unknown", "d0")}}, want: errMGetNoneRouted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newMGetFake(t)
			c := f.client(MGetConfig{})

			_, err := c.ext.mget.write(c, tt.req, c.ext.mget.fanOut)
			require.ErrorIs(t, err, tt.want)
			require.ErrorIs(t, err, errMGetNotSplittable)
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

	st, ok := c.ext.mget.windows.Stats("n1")
	require.True(t, ok)
	require.Less(t, st.Window, 8.0, "the node that answered 429 has its window shrunk below MaxInFlightDocsPerNode")
}

// TestMGetDirectMergeKeepsNodeBytes sends documents whose content would break a
// naive join: strings holding "]}" and quotes, an _id with brackets in it, HTML
// that encoding/json.Marshal would escape, the line and paragraph separators,
// escaped surrogate pairs, a node's own error item, and pretty-printed
// whitespace. The merged body must be valid JSON, be exactly the nodes' bytes in
// request order, and give each document its node's bytes untouched.
func TestMGetDirectMergeKeepsNodeBytes(t *testing.T) {
	t.Parallel()

	ids := []string{"d0", "d1", "d2", "d3", "d4", `q"]}5`}
	// escapedSmiley is U+1F600 and U+00E9 as JSON escapes: a surrogate pair and a
	// single escape, spelled out so the source shows the backslashes.
	escapedSmiley := `\` + `ud83d` + `\` + `ude00 ` + `\` + `u00e9`
	// d1's "sep" holds the characters U+2028 and U+2029 themselves, which JSON
	// allows unescaped in a string and JavaScript does not.
	want := map[string]string{
		"d0": `{"_index":"idx","_id":"d0","_version":1,"found":true,"_source":{"s":"\"]},{\"_id\":\"x\"}]}"}}`,
		"d1": `{"_index":"idx","_id":"d1","_version":1,"found":true,"_source":{"html":"<script>a && b</script>","sep":"` +
			"  " + `"}}`,
		"d2": "{ \"_index\" : \"idx\",\n  \"_id\" : \"d2\",\n  \"found\" : true,\n  \"_source\" : { \"k\" : [ 1, 2 ] }\n}",
		"d3": `{"_index":"idx","_id":"d3","_version":1,"found":true,"_source":{"escaped":"` + escapedSmiley + `","raw":"` +
			string(rune(0x1F600)) + " " + string(rune(0xE9)) + `"}}`,
		"d4":    `{"_index":"idx","_id":"d4","error":{"root_cause":[{"type":"x","reason":"\"]}"}],"type":"x","reason":"\"]}"}}`,
		`q"]}5`: `{"_index":"idx","_id":"q\"]}5","_version":1,"found":false}`,
	}

	f := newMGetFake(t)
	f.node = func(_ string, _ int, req MGetReq) (int, []string) {
		docs := make([]string, len(req.Body.Docs))
		for i, op := range req.Body.Docs {
			docs[i] = want[op.ID]
		}

		return http.StatusOK, docs
	}
	c := f.client(MGetConfig{})

	ops := mgetDocs("idx", ids...)
	resp, err := c.Doc.MGet(t.Context(), MGetReq{Body: &MGetBody{Docs: ops}})
	require.NoError(t, err)

	body := resp.Inspect().Response.RawBody()
	require.True(t, json.Valid(body), "the merged body is valid JSON: %s", body)

	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = want[id]
	}
	require.Equal(t, `{"docs":[`+strings.Join(parts, ",")+`]}`, string(body), "the body is the nodes' bytes, in request order")

	require.Len(t, resp.Docs, len(ids))
	for i, id := range ids {
		require.Equal(t, want[id], string(resp.Docs[i].RawJSON()), "document %q keeps its node's bytes", id)
		require.Equal(t, id, mgetItemID(&resp.Docs[i]), "position %d", i)
	}
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
			c.ext.mget.observer = obs
			resp, err := c.Doc.MGet(t.Context(), MGetReq{Body: &MGetBody{Docs: ops}})
			if tt.wantErr {
				require.Error(t, err)
				require.NotNil(t, resp.Inspect().Response, "the failed response is returned, as the coordinator path returns it")
				require.Equal(t, http.StatusServiceUnavailable, resp.Inspect().Response.StatusCode)

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

func TestMGetDirectResendsWhenACoordinatorAnswerCannotBePlaced(t *testing.T) {
	t.Parallel()

	ops := mgetDocs("idx", "d0", "d1", "d2", "d3")
	f := newMGetFake(t)
	f.node = func(_ string, _ int, req MGetReq) (int, []string) {
		return http.StatusOK, []string{mgetFound("idx", "wrong", "node")}[:min(1, len(req.Body.Docs))]
	}
	f.coord = func(body MGetBody) (int, []string) {
		if len(body.Docs) == len(ops) {
			return 0, nil
		}

		return http.StatusOK, []string{mgetFound("idx", "wrong", "coordinator")}
	}
	obs := &recordingMGetObserver{}
	c := f.client(MGetConfig{Observer: obs})

	resp, err := c.Doc.MGet(t.Context(), MGetReq{Body: &MGetBody{Docs: ops}})
	require.NoError(t, err)
	requireAnswers(t, ops, resp)
	for i := range resp.Docs {
		_, gerr := resp.Docs[i].GetResult()
		require.NoError(t, gerr, "document %d is the cluster's answer, not a made-up failure", i)
	}
	require.True(t, obs.done()[0].Resent)
	require.Zero(t, obs.done()[0].FailedDocs)
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
	// The fake's three shards are covered by two nodes.
	require.Equal(t, MGetDoneEvent{Docs: 6, Nodes: 2, Requests: 6}, MGetDoneEvent{
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

func TestMGetDriverSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config MGetDriver
		ctx    MGetDriver
		want   MGetDriver
	}{
		{name: "the release default", want: defaultMGetDriver},
		{name: "the config", config: MGetDriverDirect, want: MGetDriverDirect},
		{name: "the config's coordinator", config: MGetDriverCoordinator, want: MGetDriverCoordinator},
		{name: "a context override", config: MGetDriverDirect, ctx: MGetDriverCoordinator, want: MGetDriverCoordinator},
		{name: "a default context keeps the config", config: MGetDriverDirect, ctx: MGetDriverDefault, want: MGetDriverDirect},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := newMGetDriver(MGetConfig{Driver: tt.config}, nil)
			ctx := t.Context()
			if tt.ctx != MGetDriverDefault {
				ctx = WithMGetDriver(ctx, tt.ctx)
			}
			require.Equal(t, tt.want, d.selected(ctx))
		})
	}
}

func TestParseMGetDriver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    MGetDriver
		wantErr bool
	}{
		{in: "", want: MGetDriverDefault},
		{in: "direct", want: MGetDriverDirect},
		{in: " Direct ", want: MGetDriverDirect},
		{in: "COORDINATOR", want: MGetDriverCoordinator},
		{in: "shard", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			got, err := ParseMGetDriver(tt.in)
			if tt.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			if tt.want != MGetDriverDefault {
				require.Equal(t, strings.ToLower(strings.TrimSpace(tt.in)), got.String())
			}
		})
	}
}

// mgetCoverTargets returns the copies of shard s, laid out round-robin over
// conns.
func mgetCoverTargets(conns []*opensearchtransport.Connection, s, copies int) []*opensearchtransport.ShardTarget {
	targets := make([]*opensearchtransport.ShardTarget, copies)
	for j := range targets {
		targets[j] = &opensearchtransport.ShardTarget{Shard: s, Node: conns[(s+j)%len(conns)], Primary: j == 0}
	}

	return targets
}

//nolint:paralleltest // testing.AllocsPerRun panics in a parallel test.
func TestMGetCoverReuseDoesNotAllocate(t *testing.T) {
	const nodes, shards, docs = 30, 90, 300
	conns := make([]*opensearchtransport.Connection, nodes)
	for i := range conns {
		conns[i] = &opensearchtransport.Connection{Name: fmt.Sprintf("n%02d", i)}
	}
	targets := make([][]*opensearchtransport.ShardTarget, shards)
	for s := range targets {
		targets[s] = mgetCoverTargets(conns, s, 2)
	}
	windows := newMGetDriver(MGetConfig{}, nil).windows
	notSaturated := func(*opensearchtransport.Connection) bool { return false }
	picks := make([]shardNode, docs)

	c := newMGetCover()
	run := func() {
		c.prepare(docs)
		for i := range docs {
			if i%50 == 0 {
				c.contacted(conns[i%nodes].Name)
			}
			require.True(t, c.add(i, "idx", targets[i%shards], "", notSaturated))
		}
		c.assign(windows, picks)
		c.recycle()
	}
	run()

	require.Zero(t, testing.AllocsPerRun(20, run), "a recycled cover reuses everything it grew")
	for i := range picks {
		require.NotNil(t, picks[i].node, "position %d", i)
	}
}

func TestMGetCoverRecycleDropsWhatOutgrewItsBounds(t *testing.T) {
	t.Parallel()

	conns := make([]*opensearchtransport.Connection, mgetCoverMaxNodes+1)
	for i := range conns {
		conns[i] = &opensearchtransport.Connection{Name: strconv.Itoa(i)}
	}
	notSaturated := func(*opensearchtransport.Connection) bool { return false }

	c := newMGetCover()
	c.prepare(len(conns))
	for i := range conns {
		require.True(t, c.add(i, "idx", mgetCoverTargets(conns, i, 1), "", notSaturated))
	}
	c.assign(newMGetDriver(MGetConfig{}, nil).windows, make([]shardNode, len(conns)))
	c.recycle()

	require.Empty(t, c.nodes)
	require.Empty(t, c.index)
	require.Empty(t, c.spare, "the nodes of a cover that outgrew its bounds are not kept")
	for _, shard := range c.shards[:cap(c.shards)] {
		require.Nil(t, shard.chosen, "a recycled cover keeps no connection alive")
	}
}

func TestMGetFanOutSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config MGetFanOut
		ctx    MGetFanOut
		want   MGetFanOut
	}{
		{name: "the release default", want: MGetFanOutFewestNodes},
		{name: "the config", config: MGetFanOutLeastLoaded, want: MGetFanOutLeastLoaded},
		{name: "a context override", config: MGetFanOutLeastLoaded, ctx: MGetFanOutFewestNodes, want: MGetFanOutFewestNodes},
		{name: "a default context keeps the config", config: MGetFanOutLeastLoaded, ctx: MGetFanOutDefault, want: MGetFanOutLeastLoaded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := newMGetDriver(MGetConfig{FanOut: tt.config}, nil)
			ctx := t.Context()
			if tt.ctx != MGetFanOutDefault {
				ctx = WithMGetFanOut(ctx, tt.ctx)
			}
			require.Equal(t, tt.want, d.selectedFanOut(ctx))
		})
	}
}

func TestParseMGetFanOut(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    MGetFanOut
		wantErr bool
	}{
		{in: "", want: MGetFanOutDefault},
		{in: "fewest-nodes", want: MGetFanOutFewestNodes},
		{in: " Fewest-Nodes ", want: MGetFanOutFewestNodes},
		{in: "LEAST-LOADED", want: MGetFanOutLeastLoaded},
		{in: "random", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			got, err := ParseMGetFanOut(tt.in)
			if tt.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			if tt.want != MGetFanOutDefault {
				require.Equal(t, strings.ToLower(strings.TrimSpace(tt.in)), got.String())
			}
		})
	}
}

// TestMGetChoiceEnv checks that OPENSEARCH_GO_MGET and OPENSEARCH_GO_MGET_FANOUT
// override the configuration, and that a value that does not parse, or spells
// the Default, leaves the configuration's choice in place.
func TestMGetChoiceEnv(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		config     MGetConfig
		wantDriver MGetDriver
		wantFanOut MGetFanOut
	}{
		{
			name:       "unset, the release defaults",
			wantDriver: defaultMGetDriver, wantFanOut: defaultMGetFanOut,
		},
		{
			name:       "set, over the configuration",
			env:        map[string]string{envvars.MGet: "direct", envvars.MGetFanOut: "least-loaded"},
			config:     MGetConfig{Driver: MGetDriverCoordinator, FanOut: MGetFanOutFewestNodes},
			wantDriver: MGetDriverDirect, wantFanOut: MGetFanOutLeastLoaded,
		},
		{
			name:       "set, over the release defaults",
			env:        map[string]string{envvars.MGet: " Direct ", envvars.MGetFanOut: "LEAST-LOADED"},
			wantDriver: MGetDriverDirect, wantFanOut: MGetFanOutLeastLoaded,
		},
		{
			name:       "a value that does not parse keeps the configuration",
			env:        map[string]string{envvars.MGet: "shard", envvars.MGetFanOut: "random"},
			config:     MGetConfig{Driver: MGetDriverDirect, FanOut: MGetFanOutLeastLoaded},
			wantDriver: MGetDriverDirect, wantFanOut: MGetFanOutLeastLoaded,
		},
		{
			name:       "an empty value keeps the release defaults",
			env:        map[string]string{envvars.MGet: "", envvars.MGetFanOut: ""},
			wantDriver: defaultMGetDriver, wantFanOut: defaultMGetFanOut,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, name := range []string{envvars.MGet, envvars.MGetFanOut} {
				if v, ok := tt.env[name]; ok {
					t.Setenv(name, v)
				} else {
					// t.Setenv restores the variable after the test; unsetting it
					// tests the unset case whatever the process environment holds.
					t.Setenv(name, "")
					require.NoError(t, os.Unsetenv(name))
				}
			}

			d := newMGetDriver(tt.config, nil)
			require.Equal(t, tt.wantDriver, d.driver)
			require.Equal(t, tt.wantFanOut, d.fanOut)
		})
	}
}

// TestMGetChoiceString checks the names String gives a value no constant has.
func TestMGetChoiceString(t *testing.T) {
	t.Parallel()

	require.Equal(t, "MGetDriver(9)", MGetDriver(9).String())
	require.Equal(t, "MGetFanOut(7)", MGetFanOut(7).String())
	require.Equal(t, "default", MGetDriverDefault.String())
	require.Equal(t, "default", MGetFanOutDefault.String())
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

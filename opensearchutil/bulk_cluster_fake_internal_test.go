// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/scratch"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// fakeCluster is a ShardRouter over a made-up cluster. Where a shard lives can
// be changed while a bulk client runs, which is how a test relocates shards.
type fakeCluster struct {
	mu struct {
		sync.Mutex
		maps map[string]ShardMap
		// place returns the node hosting a shard; empty means unknown.
		place func(index string, shard int) string
		// nodes is the ranking WriteNodes returns.
		nodes []Node
		// unknown lists indexes LookupShard does not answer for.
		unknown map[string]struct{}
		// generation is the geometry generation LookupShard reports per index; 0 for
		// an index setShardMap has not been called for.
		generation map[string]uint64
		lookups    int
	}
}

func newFakeCluster(maps map[string]ShardMap, nodeNames ...string) *fakeCluster {
	c := &fakeCluster{}
	c.mu.maps = maps
	c.mu.unknown = map[string]struct{}{}
	c.mu.generation = map[string]uint64{}
	for _, name := range nodeNames {
		c.mu.nodes = append(c.mu.nodes, Node{Name: name})
	}

	c.mu.place = func(index string, shard int) string {
		if len(nodeNames) == 0 {
			return ""
		}

		return nodeNames[shard%len(nodeNames)]
	}

	return c
}

func (c *fakeCluster) LookupShard(index, key string, dst *[]*ShardTarget) error {
	*dst = (*dst)[:0]

	c.mu.Lock()
	defer c.mu.Unlock()

	c.mu.lookups++
	m, ok := c.mu.maps[index]
	if _, unknown := c.mu.unknown[index]; !ok || unknown {
		return ErrShardMapUnknown
	}

	shard := m.Shard(key)
	target := scratch.Next(dst)
	*target = ShardTarget{Shard: shard, Primary: true, Generation: c.mu.generation[index]}
	if name := c.mu.place(index, shard); name != "" {
		target.Node = Node{Name: name, Pool: c.poolOf(name)}
	}

	return nil
}

// lookupOne returns the first copy of the shard key lives on, and whether router knows it.
func lookupOne(router ShardRouter, index, key string) (ShardTarget, bool) {
	targets := shardTargetPool.Get()
	defer shardTargetPool.Put(targets)

	if err := router.LookupShard(index, key, targets); err != nil || len(*targets) == 0 {
		return ShardTarget{}, false
	}

	return *(*targets)[0], true
}

func (c *fakeCluster) WriteNodes() []Node {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]Node(nil), c.mu.nodes...)
}

// poolOf returns the pool function of the named node as WriteNodes lists it.
func (c *fakeCluster) poolOf(name string) func() opensearchtransport.PoolWindow {
	for _, n := range c.mu.nodes {
		if n.Name == name {
			return n.Pool
		}
	}

	return nil
}

// setPlacement changes where shards live.
func (c *fakeCluster) setPlacement(place func(index string, shard int) string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.mu.place = place
}

// setWindow gives every listed node a write pool window, and reports each node as
// refusing writes when writeOverloaded is set.
func (c *fakeCluster) setWindow(window int, writeOverloaded bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range c.mu.nodes {
		c.mu.nodes[i].Pool = func() opensearchtransport.PoolWindow {
			return opensearchtransport.PoolWindow{Window: window, MaxWindow: window, Overloaded: writeOverloaded}
		}
	}
}

// setShardMap gives an index a shard map, as the cluster does when the index is
// created or recreated, and moves its geometry generation on.
//
//nolint:unparam // A helper for tests that each use one index.
func (c *fakeCluster) setShardMap(index string, m ShardMap) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.mu.maps[index] = m
	c.mu.generation[index]++
}

//nolint:unparam // A helper for tests that each use one index.
func (c *fakeCluster) setUnknown(index string, unknown bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if unknown {
		c.mu.unknown[index] = struct{}{}
	} else {
		delete(c.mu.unknown, index)
	}
}

// shardOf is the shard a recorded action belongs to, as the server computes it.
func (c *fakeCluster) shardOf(a recordedAction, requestRouting string) (shardKey, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	m, ok := c.mu.maps[a.meta.Index]
	if !ok {
		return shardKey{}, false
	}

	routing := a.meta.DocumentID
	switch {
	case a.meta.Routing != nil:
		routing = *a.meta.Routing
	case requestRouting != "":
		routing = requestRouting
	}
	if routing == "" {
		return shardKey{}, false
	}

	return shardKey{index: a.meta.Index, shard: m.Shard(routing)}, true
}

// sentRequest is one bulk request the fake server received.
type sentRequest struct {
	node    string
	actions []recordedAction
	shards  map[shardKey]struct{}
	bytes   int
}

// fakeServer answers bulk requests and records them. Optionally it holds each
// request until the gate opens, and it notes whenever two requests that carry
// the same shard are in flight together.
type fakeServer struct {
	cluster *fakeCluster

	// gate, when non-nil, holds every bulk request until it is closed.
	gate chan struct{}
	// entered, when non-nil, receives the node of every bulk request as it arrives.
	entered chan string
	// yields makes each request give up the processor this many times while it is
	// in flight, so overlapping requests overlap.
	yields int
	// holdUntil, when above 0, holds every bulk request until that many are in
	// flight together, then lets them all through; see holdForPeak.
	holdUntil int
	// reached is closed once holdUntil requests have been in flight together,
	// or once holdTimeout has passed without that.
	reached     chan struct{}
	reachedOnce sync.Once
	// respond, when set, supplies the response; the default is an empty success.
	respond func(req sentRequest, body string) (status int, resp string)
	// advance, when set, is called with each request as it is served, before it
	// returns, so a test can move a fake clock by the time the request "took".
	advance func(req sentRequest)

	mu struct {
		sync.Mutex
		sent        []sentRequest
		busy        map[shardKey]int
		overlaps    int
		inflight    int
		maxInflight int
	}
}

func newFakeServer(cluster *fakeCluster) *fakeServer {
	s := &fakeServer{cluster: cluster, reached: make(chan struct{})}
	s.mu.busy = map[shardKey]int{}

	return s
}

// holdTimeout bounds how long holdForPeak waits for the peak. A client that
// never sends holdUntil requests at once is let through after it, so the test
// fails on the peak it measured rather than hanging.
const holdTimeout = 10 * time.Second

// holdForPeak holds a request until holdUntil requests are in flight together.
// The first time that happens every held request goes on, and none is held
// again, so the client's own limit is all that bounds the requests after it.
func (s *fakeServer) holdForPeak(inflight int) {
	if s.holdUntil <= 0 {
		return
	}
	if inflight >= s.holdUntil {
		s.reachedOnce.Do(func() { close(s.reached) })
	}

	timer := time.NewTimer(holdTimeout)
	defer timer.Stop()

	select {
	case <-s.reached:
	case <-timer.C:
		s.reachedOnce.Do(func() { close(s.reached) })
	}
}

func (s *fakeServer) roundTrip(req *http.Request) (*http.Response, error) {
	if !strings.HasSuffix(req.URL.Path, "/_bulk") {
		return defaultRoundTripFunc(req)
	}

	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}

	sent := sentRequest{
		node:    FlushTarget(req.Context()),
		actions: parseRecordedActions(string(raw)),
		shards:  map[shardKey]struct{}{},
		bytes:   len(raw),
	}
	for _, a := range sent.actions {
		if key, ok := s.cluster.shardOf(a, req.URL.Query().Get("routing")); ok {
			sent.shards[key] = struct{}{}
		}
	}

	s.mu.Lock()
	for key := range sent.shards {
		if s.mu.busy[key] > 0 {
			s.mu.overlaps++
		}
		s.mu.busy[key]++
	}
	s.mu.inflight++
	s.mu.maxInflight = max(s.mu.maxInflight, s.mu.inflight)
	inflight := s.mu.inflight
	s.mu.sent = append(s.mu.sent, sent)
	s.mu.Unlock()

	s.holdForPeak(inflight)
	if s.entered != nil {
		s.entered <- sent.node
	}
	if s.gate != nil {
		<-s.gate
	}
	for range s.yields {
		runtime.Gosched()
	}

	if s.advance != nil {
		s.advance(sent)
	}

	s.mu.Lock()
	for key := range sent.shards {
		s.mu.busy[key]--
	}
	s.mu.inflight--
	s.mu.Unlock()

	if s.respond != nil {
		status, body := s.respond(sent, string(raw))

		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
	}

	return bulkOKResponse(len(sent.actions)), nil
}

func (s *fakeServer) requests() []sentRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]sentRequest(nil), s.mu.sent...)
}

func (s *fakeServer) overlaps() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.mu.overlaps
}

func (s *fakeServer) maxInflight() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.mu.maxInflight
}

// newBulkClient builds a bulk client over the fake cluster and server.
func newBulkClient(t *testing.T, server *fakeServer, cfg BulkIndexerConfig) BulkIndexer {
	t.Helper()

	cfg.Client = newBulkTestClient(t, server.roundTrip)
	if cfg.ShardRouter == nil {
		cfg.ShardRouter = server.cluster
	}

	bi, err := NewBulkIndexer(cfg)
	require.NoError(t, err)

	return bi
}

// addDoc adds a document with a sequence number as its body.
func addDoc(t *testing.T, bi BulkIndexer, index, id string, seq int) {
	t.Helper()

	require.NoError(t, bi.Add(t.Context(), BulkIndexerItem{
		Index: index, Action: actionIndex, DocumentID: id, Body: seqBody(seq),
	}))
}

// recordedAction is one action line of a bulk request, with the sequence number
// its source carried.
type recordedAction struct {
	action  string
	meta    bulkActionMetadata
	seq     int
	hasBody bool
}

// parseRecordedActions reads the NDJSON of a bulk request: an action line,
// followed by a source line that carries {"seq":N} for every action but a delete.
func parseRecordedActions(body string) []recordedAction {
	var actions []recordedAction

	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		var envelope map[string]bulkActionMetadata
		if err := json.Unmarshal([]byte(lines[i]), &envelope); err != nil {
			continue
		}

		for action, meta := range envelope {
			rec := recordedAction{action: action, meta: meta}
			if action != actionDelete && i+1 < len(lines) {
				var src struct {
					Seq int `json:"seq"`
				}
				if json.Unmarshal([]byte(lines[i+1]), &src) == nil {
					rec.seq, rec.hasBody = src.Seq, true
					i++
				}
			}
			actions = append(actions, rec)
		}
	}

	return actions
}

func seqBody(seq int) *strings.Reader {
	return strings.NewReader(fmt.Sprintf(`{"seq":%d}`, seq))
}

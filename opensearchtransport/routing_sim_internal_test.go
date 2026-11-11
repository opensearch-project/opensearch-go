// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"container/heap"
	"fmt"
	"hash/fnv"
	"maps"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// hclQuantile is Prequal's Q_RIF: a candidate is hot when its requests in flight
// exceed this quantile of the candidates' values.
const hclQuantile = 0.75

// hclQueue returns the polled queue size of c, zero before the first poll.
func hclQueue(c *Connection) float64 {
	if snap := c.ars.p.Load(); snap != nil {
		return float64(snap.reading.AvgQueueSize)
	}
	return 0
}

// hclSelect is Prequal's hot-cold lexicographic rule: among the candidates whose
// RIF (client in-flight plus polled queue) is not above the hclQuantile quantile
// of the candidates' RIFs, pick the one with the lowest latency. Ties break
// uniformly at random. The candidate with the lowest RIF is never hot, so a cold
// one always exists.
func hclSelect(cands []*Connection, rng *rand.Rand) *Connection {
	rif := make([]float64, len(cands))
	for i, c := range cands {
		rif[i] = float64(c.loadInFlight(poolSearch)) + hclQueue(c)
	}
	sorted := slices.Sorted(slices.Values(rif))
	pos := hclQuantile * float64(len(sorted)-1)
	lo := int(pos)
	q := sorted[lo]
	if lo+1 < len(sorted) {
		q += (pos - float64(lo)) * (sorted[lo+1] - sorted[lo])
	}

	best := make([]*Connection, 0, len(cands))
	bestLatency := math.Inf(1)
	for i, c := range cands {
		if rif[i] > q {
			continue
		}
		switch lat := c.loadLatencyMicros(poolSearch); {
		case lat < bestLatency:
			best, bestLatency = append(best[:0], c), lat
		case lat == bestLatency:
			best = append(best, c)
		}
	}
	return best[rng.IntN(len(best))]
}

func TestHCLSelect(t *testing.T) {
	t.Parallel()

	type node struct {
		inFlight int32
		queue    int64
		rtt      time.Duration
	}
	const fast, mid, slow = 200 * time.Microsecond, 500 * time.Microsecond, time.Millisecond
	tests := []struct {
		name  string
		nodes []node
		want  []int // indices that may be picked; every one must be picked over many draws
	}{
		{
			name:  "all equal picks among all",
			nodes: []node{{0, 0, fast}, {0, 0, fast}, {0, 0, fast}},
			want:  []int{0, 1, 2},
		},
		{
			name:  "hot node is skipped despite the lowest latency",
			nodes: []node{{10, 0, fast}, {1, 0, slow}, {0, 1, fast}, {1, 0, slow}},
			want:  []int{2},
		},
		{
			name:  "queue counts toward RIF",
			nodes: []node{{0, 10, fast}, {1, 0, mid}, {1, 0, slow}, {0, 1, slow}},
			want:  []int{1},
		},
		{
			name:  "latency tie among the cold breaks at random",
			nodes: []node{{1, 0, fast}, {0, 1, fast}, {1, 0, slow}, {20, 0, fast}},
			want:  []int{0, 1},
		},
		{
			name:  "single candidate",
			nodes: []node{{5, 5, slow}},
			want:  []int{0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cands := make([]*Connection, len(tt.nodes))
			for i, n := range tt.nodes {
				c := scoreTestConn(t, "n"+strconv.Itoa(i), n.rtt, 0)
				c.setLifecycleBit(lcActive)
				c.pools.getForScoring(poolSearch).inFlight.Store(n.inFlight)
				recordUsable(&c.ars, time.Now(), c.ID, c.ID, len(tt.nodes), AdaptiveSelectionStats{AvgQueueSize: n.queue})
				cands[i] = c
			}

			rng := rand.New(rand.NewPCG(1, 2))
			got := map[int]struct{}{}
			for range 200 {
				got[slices.Index(cands, hclSelect(cands, rng))] = struct{}{}
			}
			require.ElementsMatch(t, tt.want, slices.Collect(maps.Keys(got)))
		})
	}
}

// Simulation of one cluster served by several clients, in virtual milliseconds.
const (
	simNodes      = 5
	simWorkers    = 4
	simClients    = 8
	simRTT        = 0.5    // ms, client to node and back
	simPollEvery  = 5000.0 // ms
	simWindow     = 2000.0 // ms, antagonist alternates between normal and slow
	simSlowdown   = 3.0    // antagonist service-time factor in its slow window
	simRequests   = 40000  // per run
	simWarmup     = simRequests / 10
	simEWMAAlpha  = 0.3              // node-side queue and service-time averages
	simBackdate   = 11 * time.Second // moves the first poll past the adaptive-selection blackout
	simSpreadBand = 0.25

	// simRelativeSlack is how far above hot-cold's p99 c3+spread may sit.
	simRelativeSlack = 1.10
)

// simC3P99Bound is the p99 ms limit on c3+spread at each load, about twice the
// highest value recorded across simSeeds (55.75 at 0.9, from seed 42).
var simC3P99Bound = map[float64]float64{0.7: 31, 0.9: 112}

// simSeeds are the seeds every guard in TestRoutingSim runs on; the first one's
// results are logged.
var simSeeds = []uint64{42, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

// simMeanService is the mean service time of each node in ms; the last node is the
// antagonist, whose nominal mean is its fast value.
var simMeanService = [simNodes]float64{2, 2, 3, 4, 2}

// simCapacity is the cluster capacity in requests per ms. The antagonist counts at
// its rate averaged over the duty cycle: full rate in its normal window and
// 1/simSlowdown of it in its slow window.
func simCapacity() float64 {
	var c float64
	for i, m := range simMeanService {
		r := simWorkers / m
		if i == simNodes-1 {
			r = (r + r/simSlowdown) / 2
		}
		c += r
	}
	return c
}

func TestSimCapacity(t *testing.T) {
	t.Parallel()

	// 4/2 + 4/2 + 4/3 + 4/4 for the steady nodes, plus the antagonist's rate
	// averaged over its duty cycle: (4/2 + 4/(2*3)) / 2.
	require.InDelta(t, 7.667, simCapacity(), 0.001)
}

type simRequest struct {
	client  int
	node    int
	arrived float64
}

type simServer struct {
	busy         int
	queue        []*simRequest
	avgQ, avgSvc float64
}

type simEventKind int

const (
	simArrival simEventKind = iota
	simDone
	simPoll
)

type simEvent struct {
	at   float64
	seq  int
	kind simEventKind
	req  *simRequest
	// client of a poll
	client int
}

type simEvents []simEvent

func (h simEvents) Len() int { return len(h) }
func (h simEvents) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}
func (h simEvents) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *simEvents) Push(x any)   { *h = append(*h, x.(simEvent)) }
func (h *simEvents) Pop() any {
	old := *h
	e := old[len(old)-1]
	*h = old[:len(old)-1]
	return e
}

type simClient struct {
	conns  []*Connection
	rng    *rand.Rand
	scores []float64
	c3Dead int // picks made with the C3 term not live on every candidate
}

type simResult struct {
	p50, p99, maxShare float64
	// arrivals hashes every (arrival time, client) pair, so two policies can be
	// checked for having faced the same load.
	arrivals uint64
	// c3Dead counts picks made while the C3 term was not live on all candidates.
	c3Dead int
}

// simChooser picks one of the client's connections for a request.
type simChooser func(c *simClient) *Connection

func simC3Spread(c *simClient) *Connection {
	if !newC3Inputs(c.conns, poolSearch).active {
		c.c3Dead++
	}
	return connScoreSelect(c.conns, nil, nil, &shardCostForReads, poolSearch, true, c.scores, nil, nil, nil,
		coordinatorSelection{spread: simSpreadBand, randFloat: c.rng.Float64})
}

func simHCL(c *simClient) *Connection { return hclSelect(c.conns, c.rng) }

// runRoutingSim runs simRequests Poisson arrivals at load times the cluster capacity
// through choose and reports the post-warmup latency percentiles and the busiest
// node's share of picks. Everything is driven by seed, so a run is repeatable.
func runRoutingSim(t *testing.T, seed uint64, load float64, choose simChooser) simResult {
	t.Helper()

	// One stream per concern: arrivals (gaps and client pick) never see a draw made
	// for service times, so every policy faces the same load for a seed.
	arr := rand.New(rand.NewPCG(seed, 1))
	svcRNG := rand.New(rand.NewPCG(seed, 2))
	clock := newTestClock()
	var now float64
	advance := func(to float64) {
		clock.Advance(time.Duration((to - now) * float64(time.Millisecond)))
		now = to
	}

	nodes := make([]simServer, simNodes)
	for i := range nodes {
		nodes[i].avgSvc = simMeanService[i]
	}
	clients := make([]*simClient, simClients)
	for ci := range clients {
		cl := &simClient{rng: rand.New(rand.NewPCG(seed, uint64(ci)+100)), scores: make([]float64, simNodes)}
		for ni := range simNodes {
			c := scoreTestConn(t, "n"+strconv.Itoa(ni), time.Duration(simRTT*float64(time.Millisecond)), 0)
			c.setLifecycleBit(lcActive)
			c.pools.getForScoring(poolSearch).latency.clock = clock
			c.pools.getForScoring(poolSearch).cwnd.Store(100)
			recordUsable(&c.ars, time.Now().Add(-simBackdate), c.ID, c.ID, simNodes,
				AdaptiveSelectionStats{AvgServiceTimeNs: int64(simMeanService[ni] * 1e6)})
			cl.conns = append(cl.conns, c)
		}
		clients[ci] = cl
	}

	var (
		events simEvents
		seq    int
	)
	push := func(e simEvent) {
		e.seq = seq
		seq++
		heap.Push(&events, e)
	}
	for ci := range clients {
		push(simEvent{at: simPollEvery * float64(ci+1) / simClients, kind: simPoll, client: ci})
	}
	rate := load * simCapacity()
	push(simEvent{at: arr.ExpFloat64() / rate, kind: simArrival})

	startService := func(ni int, r *simRequest) {
		mean := simMeanService[ni]
		if ni == simNodes-1 && int(now/simWindow)%2 == 1 {
			mean *= simSlowdown
		}
		svc := svcRNG.ExpFloat64() * mean
		nodes[ni].busy++
		nodes[ni].avgSvc += simEWMAAlpha * (svc - nodes[ni].avgSvc)
		push(simEvent{at: now + svc, kind: simDone, req: r})
	}

	var (
		arrived   int
		latencies []float64
		picks     [simNodes]int

		arrivalHash = fnv.New64a()
	)
	for events.Len() > 0 {
		e := heap.Pop(&events).(simEvent)
		advance(e.at)
		switch e.kind {
		case simArrival:
			ci := arr.IntN(simClients)
			cl := clients[ci]
			fmt.Fprintf(arrivalHash, "%v/%d;", now, ci)
			conn := choose(cl)
			ni := slices.Index(cl.conns, conn)
			conn.addInFlight(poolSearch)
			r := &simRequest{client: ci, node: ni, arrived: now}
			if arrived >= simWarmup {
				picks[ni]++
			}
			n := &nodes[ni]
			// Like the server's adaptive_selection EWMA, this updates only when a
			// request reaches the node, so an idle node keeps its last value.
			n.avgQ += simEWMAAlpha * (float64(len(n.queue)) - n.avgQ)
			if n.busy < simWorkers {
				startService(ni, r)
			} else {
				n.queue = append(n.queue, r)
			}
			arrived++
			if arrived < simRequests {
				push(simEvent{at: now + arr.ExpFloat64()/rate, kind: simArrival})
			}
		case simDone:
			r := e.req
			n := &nodes[r.node]
			n.busy--
			if len(n.queue) > 0 {
				next := n.queue[0]
				n.queue = n.queue[1:]
				startService(r.node, next)
			}
			conn := clients[r.client].conns[r.node]
			conn.releaseInFlight(poolSearch)
			lat := now - r.arrived + simRTT
			conn.recordLatency(poolSearch, time.Duration(lat*float64(time.Millisecond)))
			latencies = append(latencies, lat)
			if len(latencies) == simRequests {
				events = events[:0]
			}
		case simPoll:
			for ni, c := range clients[e.client].conns {
				c.ars.record(time.Now(), c.ID, c.ID, simNodes, AdaptiveSelectionStats{
					AvgQueueSize:     int64(math.Round(nodes[ni].avgQ)),
					AvgServiceTimeNs: max(int64(nodes[ni].avgSvc*1e6), 1),
				})
			}
			push(simEvent{at: now + simPollEvery, kind: simPoll, client: e.client})
		}
	}
	require.Len(t, latencies, simRequests)

	// latencies are in completion order; drop the first simWarmup completions.
	lat := slices.Clone(latencies[simWarmup:])
	slices.Sort(lat)
	var maxPicks int
	for _, p := range picks {
		maxPicks = max(maxPicks, p)
	}
	var c3Dead int
	for _, cl := range clients {
		c3Dead += cl.c3Dead
	}
	return simResult{
		p50:      lat[len(lat)/2],
		p99:      lat[len(lat)*99/100],
		maxShare: float64(maxPicks) / float64(simRequests-simWarmup),
		arrivals: arrivalHash.Sum64(),
		c3Dead:   c3Dead,
	}
}

func TestRoutingSim(t *testing.T) {
	t.Parallel()

	policies := []struct {
		name   string
		choose simChooser
	}{
		{"c3+spread", simC3Spread},
		{"hcl", simHCL},
	}

	t.Run("policies face identical arrivals", func(t *testing.T) {
		t.Parallel()
		a := runRoutingSim(t, 7, 0.7, policies[0].choose)
		b := runRoutingSim(t, 7, 0.7, policies[1].choose)
		require.Equal(t, a.arrivals, b.arrivals)
	})

	t.Run("same seed gives identical results", func(t *testing.T) {
		t.Parallel()
		for _, p := range policies {
			first := runRoutingSim(t, 7, 0.7, p.choose)
			second := runRoutingSim(t, 7, 0.7, p.choose)
			require.Equal(t, first, second, p.name)
		}
	})

	// Two guards on c3+spread, for every seed: it stays within simRelativeSlack
	// of hot-cold at each load, and under simC3P99Bound ms in absolute terms, so
	// a regression cannot hide behind a hot-cold that regressed with it. The C3
	// term must also be live on every pick. The first seed's results are logged.
	loads := []float64{0.7, 0.9}
	for i, seed := range simSeeds {
		t.Run("seed "+strconv.FormatUint(seed, 10), func(t *testing.T) {
			t.Parallel()

			results := map[string]map[float64]simResult{}
			for _, p := range policies {
				results[p.name] = map[float64]simResult{}
				for _, load := range loads {
					results[p.name][load] = runRoutingSim(t, seed, load, p.choose)
				}
			}

			if i == 0 {
				t.Logf("%-10s %5s %9s %9s %9s", "policy", "load", "p50 ms", "p99 ms", "max share")
				for _, p := range policies {
					for _, load := range loads {
						r := results[p.name][load]
						t.Logf("%-10s %5.2f %9.2f %9.2f %9.3f", p.name, load, r.p50, r.p99, r.maxShare)
					}
				}
			}

			for _, load := range loads {
				c3, hcl := results["c3+spread"][load], results["hcl"][load]
				require.LessOrEqual(t, c3.p99, hcl.p99*simRelativeSlack, "load %.2f: c3+spread p99 regressed against hot-cold", load)
				require.LessOrEqual(t, c3.p99, simC3P99Bound[load], "load %.2f: c3+spread p99 exceeds its recorded bound", load)
				require.Zero(t, c3.c3Dead, "load %.2f: picks made without a live C3 term", load)
			}
		})
	}
}

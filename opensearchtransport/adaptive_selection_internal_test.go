// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	json "github.com/opensearch-project/opensearch-go/v5/internal/jsonx"
)

func TestAdaptiveSelectionStatsUnmarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want map[string]AdaptiveSelectionStats
	}{
		{
			name: "two targets with ignored fields",
			body: `{"adaptive_selection":{
				"a":{"outgoing_searches":1,"avg_queue_size":2,"avg_service_time":"3ms",
				"avg_service_time_ns":3000000,"avg_response_time":"4ms","avg_response_time_ns":4000000,"rank":"4.0"},
				"b":{"outgoing_searches":5,"avg_queue_size":6,"avg_service_time_ns":7,"avg_response_time_ns":8,"rank":"1.5"}}}`,
			want: map[string]AdaptiveSelectionStats{
				"a": {OutgoingSearches: 1, AvgQueueSize: 2, AvgServiceTimeNs: 3000000, AvgResponseTimeNs: 4000000},
				"b": {OutgoingSearches: 5, AvgQueueSize: 6, AvgServiceTimeNs: 7, AvgResponseTimeNs: 8},
			},
		},
		{name: "field absent", body: `{}`, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var ns NodeStats
			require.NoError(t, json.Unmarshal([]byte(tt.body), &ns))
			require.Equal(t, tt.want, ns.AdaptiveSelection)
		})
	}
}

func TestAdaptiveSelection(t *testing.T) {
	t.Parallel()

	t0 := time.Unix(1_000_000, 0)
	tb := t0.Add(-time.Second) // when each reporter's baseline is sent
	s0 := AdaptiveSelectionStats{OutgoingSearches: 100}
	s1 := AdaptiveSelectionStats{OutgoingSearches: 1}
	s2 := AdaptiveSelectionStats{OutgoingSearches: 2}
	s9 := AdaptiveSelectionStats{OutgoingSearches: 9}
	type report struct {
		at   time.Time
		s    AdaptiveSelectionStats
		node string // empty reads as "a"
		from string // reporter; empty reads as node
	}
	type probe struct {
		at     time.Time
		want   bool
		wantSt AdaptiveSelectionStats
	}

	tests := []struct {
		name    string
		reports []report
		probes  []probe
	}{
		{
			name:   "never reported",
			probes: []probe{{at: t0, want: false}, {at: t0.Add(time.Hour), want: false}},
		},
		{
			name:    "a reporter's first report is only a baseline",
			reports: []report{{at: t0, s: s1}},
			probes:  []probe{{at: t0.Add(adaptiveSelectionBlackout), want: false}, {at: t0.Add(time.Hour), want: false}},
		},
		{
			name:    "blackout then usable then expiry",
			reports: []report{{at: tb, s: s0}, {at: t0, s: s1}},
			probes: []probe{
				{at: t0, want: false},
				{at: t0.Add(9900 * time.Millisecond), want: false},
				{at: t0.Add(adaptiveSelectionBlackout), want: true, wantSt: s1},
				{at: t0.Add(adaptiveSelectionExpiry), want: true, wantSt: s1},
				{at: t0.Add(adaptiveSelectionExpiry + time.Nanosecond), want: false},
			},
		},
		{
			// The silent reporter is pruned, so its next report is a new baseline.
			name: "a report after expiry is a baseline and the next change restarts blackout",
			reports: []report{
				{at: tb, s: s0},
				{at: t0, s: s1},
				{at: t0.Add(4 * time.Minute), s: s2},
				{at: t0.Add(4*time.Minute + 30*time.Second), s: s1},
			},
			probes: []probe{
				{at: t0.Add(4 * time.Minute), want: false},
				{at: t0.Add(4*time.Minute + 39*time.Second), want: false},
				{at: t0.Add(4*time.Minute + 40*time.Second), want: true, wantSt: s1},
			},
		},
		{
			name:    "identical report does not refresh lastSeen",
			reports: []report{{at: tb, s: s0}, {at: t0, s: s1}, {at: t0.Add(2 * time.Minute), s: s1}},
			probes: []probe{
				{at: t0.Add(adaptiveSelectionExpiry), want: true, wantSt: s1},
				{at: t0.Add(adaptiveSelectionExpiry + time.Nanosecond), want: false},
			},
		},
		{
			name:    "changed report keeps firstSeen and refreshes lastSeen",
			reports: []report{{at: tb, s: s0}, {at: t0, s: s1}, {at: t0.Add(2 * time.Minute), s: s2}},
			probes: []probe{
				{at: t0.Add(2 * time.Minute), want: true, wantSt: s2},
				{at: t0.Add(5 * time.Minute), want: true, wantSt: s2},
				{at: t0.Add(5*time.Minute + time.Nanosecond), want: false},
			},
		},
		{
			name: "another node at the same URL drops the old node's stats",
			reports: []report{
				{at: tb, s: s0},
				{at: t0, s: s1},
				{at: t0.Add(time.Minute), s: s1, node: "b"},
				{at: t0.Add(2 * time.Minute), s: s2, node: "b"},
			},
			probes: []probe{
				{at: t0.Add(time.Minute + adaptiveSelectionBlackout), want: false},
				{at: t0.Add(2*time.Minute + adaptiveSelectionBlackout), want: true, wantSt: s2},
			},
		},
		{
			name: "a moving peer report keeps a frozen self-report's node fresh",
			reports: []report{
				{at: tb, s: s0},
				{at: tb, s: s0, from: "p"},
				{at: t0, s: s1},
				{at: t0, s: s1, from: "p"},
				{at: t0.Add(2 * time.Minute), s: s1},
				{at: t0.Add(2 * time.Minute), s: s2, from: "p"},
			},
			probes: []probe{
				{at: t0.Add(5 * time.Minute), want: true, wantSt: s2},
				{at: t0.Add(5*time.Minute + time.Nanosecond), want: false},
			},
		},
		{
			name: "each reporter's frozen values are judged against its own",
			reports: []report{
				{at: tb, s: s0},
				{at: tb, s: s0, from: "p"},
				{at: t0, s: s1},
				{at: t0, s: s2, from: "p"},
				{at: t0.Add(2 * time.Minute), s: s1},
				{at: t0.Add(2 * time.Minute), s: s2, from: "p"},
			},
			probes: []probe{
				{at: t0.Add(adaptiveSelectionExpiry), want: true, wantSt: s2},
				{at: t0.Add(adaptiveSelectionExpiry + time.Nanosecond), want: false},
			},
		},
		{
			name:    "a new reporter's frozen values do not replace moving stats",
			reports: []report{{at: tb, s: s0}, {at: t0, s: s1}, {at: t0.Add(time.Minute), s: s9, from: "stale"}},
			probes:  []probe{{at: t0.Add(time.Minute), want: true, wantSt: s1}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var a adaptiveSelection
			for _, r := range tt.reports {
				node := r.node
				if node == "" {
					node = "a"
				}
				from := r.from
				if from == "" {
					from = node
				}
				a.record(r.at, from, node, 1, r.s)
			}
			for _, p := range tt.probes {
				got, ok := a.load(p.at)
				require.Equal(t, p.want, ok, "at %v", p.at.Sub(t0))
				require.Equal(t, p.wantSt, got.AdaptiveSelectionStats, "at %v", p.at.Sub(t0))
			}
		})
	}
}

// The stored stats carry the size of the report they came from, which C3 uses
// as the server's clientNum.
func TestAdaptiveSelectionCarriesReportSize(t *testing.T) {
	t.Parallel()

	var a adaptiveSelection
	t0 := time.Unix(1_000_000, 0)
	recordUsable(&a, t0, "p", "a", 7, AdaptiveSelectionStats{OutgoingSearches: 1})
	got, ok := a.load(t0.Add(adaptiveSelectionBlackout))
	require.True(t, ok)
	require.Equal(t, 7, got.clients)
}

func TestAdaptiveSelectionConcurrent(t *testing.T) {
	t.Parallel()

	var a adaptiveSelection
	t0 := time.Unix(1_000_000, 0)
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 1000 {
			a.record(t0.Add(time.Duration(i)*time.Second), "a", "a", 1, AdaptiveSelectionStats{OutgoingSearches: int64(i)})
		}
	})
	for range 4 {
		wg.Go(func() {
			for range 1000 {
				a.load(t0.Add(time.Minute))
			}
		})
	}
	wg.Wait()
}

func TestAdaptiveSelectionLoadAllocs(t *testing.T) {
	var a adaptiveSelection
	t0 := time.Unix(1_000_000, 0)
	recordUsable(&a, t0, "a", "a", 1, AdaptiveSelectionStats{OutgoingSearches: 1})
	require.InDelta(t, 0.0, testing.AllocsPerRun(100, func() { a.load(t0.Add(time.Minute)) }), 0)
}

func TestAdaptiveSelectionPrunesSilentReporters(t *testing.T) {
	t.Parallel()

	var a adaptiveSelection
	t0 := time.Unix(1_000_000, 0)
	a.record(t0, "gone", "a", 1, AdaptiveSelectionStats{OutgoingSearches: 1})
	a.record(t0.Add(adaptiveSelectionExpiry+time.Second), "a", "a", 1, AdaptiveSelectionStats{OutgoingSearches: 1})

	a.mu.Lock()
	defer a.mu.Unlock()
	require.Len(t, a.mu.byReporter, 1)
	require.Contains(t, a.mu.byReporter, "a")
}

// recordUsable records s from reporter, in a report of clients entries, as a
// fresh report at now, after a baseline 1ns earlier, since a reporter's first
// report is never fresh.
func recordUsable(a *adaptiveSelection, now time.Time, reporter, nodeID string, clients int, s AdaptiveSelectionStats) {
	base := s
	base.OutgoingSearches--
	a.record(now.Add(-time.Nanosecond), reporter, nodeID, clients, base)
	a.record(now, reporter, nodeID, clients, s)
}

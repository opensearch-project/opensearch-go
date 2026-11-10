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
	s1 := AdaptiveSelectionStats{OutgoingSearches: 1}
	s2 := AdaptiveSelectionStats{OutgoingSearches: 2}
	type report struct {
		at   time.Time
		s    AdaptiveSelectionStats
		node string // empty reads as "a"
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
			name:    "blackout then usable then expiry",
			reports: []report{{at: t0, s: s1}},
			probes: []probe{
				{at: t0, want: false},
				{at: t0.Add(9900 * time.Millisecond), want: false},
				{at: t0.Add(adaptiveSelectionBlackout), want: true, wantSt: s1},
				{at: t0.Add(adaptiveSelectionExpiry), want: true, wantSt: s1},
				{at: t0.Add(adaptiveSelectionExpiry + time.Nanosecond), want: false},
			},
		},
		{
			name:    "report after expiry restarts blackout",
			reports: []report{{at: t0, s: s1}, {at: t0.Add(4 * time.Minute), s: s2}},
			probes: []probe{
				{at: t0.Add(4 * time.Minute), want: false},
				{at: t0.Add(4*time.Minute + 9*time.Second), want: false},
				{at: t0.Add(4*time.Minute + 10*time.Second), want: true, wantSt: s2},
			},
		},
		{
			name:    "identical report does not refresh lastSeen",
			reports: []report{{at: t0, s: s1}, {at: t0.Add(2 * time.Minute), s: s1}},
			probes: []probe{
				{at: t0.Add(adaptiveSelectionExpiry), want: true, wantSt: s1},
				{at: t0.Add(adaptiveSelectionExpiry + time.Nanosecond), want: false},
			},
		},
		{
			name:    "changed report keeps firstSeen and refreshes lastSeen",
			reports: []report{{at: t0, s: s1}, {at: t0.Add(2 * time.Minute), s: s2}},
			probes: []probe{
				{at: t0.Add(2 * time.Minute), want: true, wantSt: s2},
				{at: t0.Add(5 * time.Minute), want: true, wantSt: s2},
				{at: t0.Add(5*time.Minute + time.Nanosecond), want: false},
			},
		},
		{
			name:    "another node at the same URL restarts blackout",
			reports: []report{{at: t0, s: s1}, {at: t0.Add(time.Minute), s: s1, node: "b"}},
			probes: []probe{
				{at: t0.Add(time.Minute), want: false},
				{at: t0.Add(time.Minute + adaptiveSelectionBlackout), want: true, wantSt: s1},
			},
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
				a.record(r.at, node, r.s)
			}
			for _, p := range tt.probes {
				got, ok := a.load(p.at)
				require.Equal(t, p.want, ok, "at %v", p.at.Sub(t0))
				require.Equal(t, p.wantSt, got, "at %v", p.at.Sub(t0))
			}
		})
	}
}

func TestAdaptiveSelectionConcurrent(t *testing.T) {
	t.Parallel()

	var a adaptiveSelection
	t0 := time.Unix(1_000_000, 0)
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 1000 {
			a.record(t0.Add(time.Duration(i)*time.Second), "a", AdaptiveSelectionStats{OutgoingSearches: int64(i)})
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
	a.record(t0, "a", AdaptiveSelectionStats{OutgoingSearches: 1})
	require.InDelta(t, 0.0, testing.AllocsPerRun(100, func() { a.load(t0.Add(time.Minute)) }), 0)
}

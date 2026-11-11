// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport //nolint:testpackage // benchmarks need internal access

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

// BenchmarkCalcMultiKeyCost measures the per-request cost of multi-value
// routing key resolution. This is the hot path for ?routing=k1,k2,k3 queries.
//
// Run with:
//
//	go test -run '^$' -bench 'BenchmarkCalcMultiKeyCost' -benchmem ./opensearchtransport/
func BenchmarkCalcMultiKeyCost(b *testing.B) {
	sm := shardMap5x6()
	slot := &indexSlot{clock: realClock{}}
	slot.shardMap.Store(sm)

	conns := make([]*Connection, 8)
	for i := range conns {
		name := fmt.Sprintf("n%d", i)
		u := &url.URL{Scheme: "https", Host: name + ":9200"}
		c := &Connection{URL: u, URLString: u.String(), Name: name, ID: name, rttRing: newRTTRing(4)}
		c.setLifecycleBit(lcActive)
		for range 4 {
			c.rttRing.add(200 * time.Microsecond)
		}
		conns[i] = c
	}

	// Calibrated prefixes for shardMap5x6 (5 shards, routingNumShards=5).
	shardPrefixes := []string{"s5a0", "s5a1", "s5a2", "s5a4", "s5a12"}

	// Build routing values for different key counts.
	buildRoutingValue := func(numKeys int) string {
		parts := make([]string, numKeys)
		for i := range numKeys {
			shard := i % 5
			parts[i] = fmt.Sprintf("%s-0", shardPrefixes[shard])
		}
		return strings.Join(parts, routingValueSeparator)
	}

	for _, numKeys := range []int{2, 3, 5, 10} {
		routingValue := buildRoutingValue(numKeys)

		b.Run(fmt.Sprintf("keys=%d", numKeys), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				candidates, locality, _ := calcMultiKeyCost(routingFeatures(0), slot, routingValue, 1, conns)
				candidates.Release()
				locality.Release()
			}
		})
	}
}

// BenchmarkCalcSingleKeyCost measures the single-key fast path for comparison.
func BenchmarkCalcSingleKeyCost(b *testing.B) {
	sm := shardMap5x6()
	slot := &indexSlot{clock: realClock{}}
	slot.shardMap.Store(sm)

	conns := make([]*Connection, 8)
	for i := range conns {
		name := fmt.Sprintf("n%d", i)
		u := &url.URL{Scheme: "https", Host: name + ":9200"}
		c := &Connection{URL: u, URLString: u.String(), Name: name, ID: name, rttRing: newRTTRing(4)}
		c.setLifecycleBit(lcActive)
		for range 4 {
			c.rttRing.add(200 * time.Microsecond)
		}
		conns[i] = c
	}

	routingValue := "s5a0-0" // routes to shard 0

	b.ReportAllocs()
	for b.Loop() {
		buf, _, _, _ := calcSingleKeyCost(routingFeatures(0), slot, routingValue, "", conns)
		buf.Release()
	}
}

// BenchmarkConnScoreSelect measures the selection+scoring phase with pooled
// scores buffer.
func BenchmarkConnScoreSelect(b *testing.B) {
	sm := shardMap5x6()
	slot := &indexSlot{clock: realClock{}}
	slot.shardMap.Store(sm)

	conns := make([]*Connection, 8)
	for i := range conns {
		name := fmt.Sprintf("n%d", i)
		u := &url.URL{Scheme: "https", Host: name + ":9200"}
		c := &Connection{URL: u, URLString: u.String(), Name: name, ID: name, rttRing: newRTTRing(4)}
		c.setLifecycleBit(lcActive)
		for range 4 {
			c.rttRing.add(time.Duration(100+i*50) * time.Microsecond)
		}
		conns[i] = c
	}

	shardInfo := make(map[string]*shardNodeInfo, 8)
	for i := range 8 {
		shardInfo[fmt.Sprintf("n%d", i)] = &shardNodeInfo{Replicas: 1}
	}
	slot.shardNodeNames.Store(&shardInfo)

	b.Run("8_candidates_no_locality", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			scores := acquireFloats(len(conns))
			_ = connScoreSelect(conns, slot, nil, &shardCostForReads, "", true,
				scores.Slice(), nil, nil, nil, coordinatorSelection{})
			scores.Release()
		}
	})

	locality := make([]float64, 8)
	for i := range locality {
		locality[i] = 1 + 0.5*float64(max(4-i, 0))/4
	}

	b.Run("8_candidates_with_locality", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			scores := acquireFloats(len(conns))
			_ = connScoreSelect(conns, slot, nil, &shardCostForReads, "", true,
				scores.Slice(), nil, locality, nil, coordinatorSelection{})
			scores.Release()
		}
	})

	b.Run("8_candidates_with_locality_and_spread", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			scores := acquireFloats(len(conns))
			_ = connScoreSelect(conns, slot, nil, &shardCostForReads, "", true,
				scores.Slice(), nil, locality, nil, coordinatorSelection{spread: defaultSpread})
			scores.Release()
		}
	})

	// The same candidates on the search pool, each with fresh adaptive_selection
	// stats, so the C3 term applies; the skipC3 case is the same work on the
	// linear term.
	for i, c := range conns {
		c.pools.getOrCreate(poolSearch).cwnd.Store(16)
		recordUsable(&c.ars, time.Now().Add(-time.Minute), c.ID, c.ID, len(conns),
			AdaptiveSelectionStats{AvgQueueSize: int64(i % 3), AvgServiceTimeNs: int64(time.Millisecond)})
	}
	if !newC3Inputs(conns, poolSearch).active {
		b.Fatal("C3 inactive")
	}
	for _, bc := range []struct {
		name string
		sel  coordinatorSelection
	}{
		{"8_candidates_search_c3_and_spread", coordinatorSelection{spread: defaultSpread}},
		{"8_candidates_search_linear_and_spread", coordinatorSelection{spread: defaultSpread, skipC3: true}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				scores := acquireFloats(len(conns))
				_ = connScoreSelect(conns, slot, nil, &shardCostForReads, poolSearch, true,
					scores.Slice(), nil, locality, nil, bc.sel)
				scores.Release()
			}
		})
	}
}

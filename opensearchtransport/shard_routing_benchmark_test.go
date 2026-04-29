// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

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
		c.state.Store(int64(newConnState(lcActive)))
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
			b.ResetTimer()
			for range b.N {
				candidates, extraCost := calcMultiKeyCost(routingFeatures(0), slot, routingValue, conns)
				candidates.Release()
				extraCost.Release()
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
		c.state.Store(int64(newConnState(lcActive)))
		for range 4 {
			c.rttRing.add(200 * time.Microsecond)
		}
		conns[i] = c
	}

	routingValue := "s5a0-0" // routes to shard 0

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		candidates, _, _ := calcSingleKeyCost(routingFeatures(0), slot, routingValue, conns)
		_ = candidates
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
		c.state.Store(int64(newConnState(lcActive)))
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

	b.Run("8_candidates_no_extraCost", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			scores := acquireFloats(len(conns))
			_ = connScoreSelect(conns, slot, nil, &shardCostForReads, "", true,
				scores.Slice(), nil, nil)
			scores.Release()
		}
	})

	extraCost := make([]float64, 8)
	for i := range extraCost {
		extraCost[i] = float64(4 - i)
		if extraCost[i] < 0 {
			extraCost[i] = 0
		}
	}

	b.Run("8_candidates_with_extraCost", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			scores := acquireFloats(len(conns))
			_ = connScoreSelect(conns, slot, nil, &shardCostForReads, "", true,
				scores.Slice(), nil, extraCost)
			scores.Release()
		}
	})
}

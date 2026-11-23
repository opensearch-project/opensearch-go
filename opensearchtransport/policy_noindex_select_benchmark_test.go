// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport //nolint:testpackage // benchmarks need internal access

import (
	"fmt"
	"net/url"
	"testing"
	"time"
)

// BenchmarkNoIndexSelect compares the old no-index selection, which ranks every
// connection with connScoreSelect, with selectNoIndex, on connection lists of
// one, two and three RTT tiers of 20 nodes each.
func BenchmarkNoIndexSelect(b *testing.B) {
	for _, n := range []int{2, 3, 8, 30, 60} {
		conns := make([]*Connection, n)
		for i := range conns {
			name := fmt.Sprintf("n%02d", i)
			u := &url.URL{Scheme: "https", Host: name + ":9200"}
			c := &Connection{URL: u, URLString: u.String(), Name: name, ID: name, rttRing: newRTTRing(4)}
			c.setLifecycleBit(lcActive)
			rtt := time.Duration(300*(1+i/20)) * time.Microsecond // a tier per 20 nodes
			for range 4 {
				c.rttRing.add(rtt)
			}
			c.pools.getOrCreate(poolWrite).cwnd.Store(16)
			conns[i] = c
		}
		sortConnectionsByRTT(conns)
		var cluster clusterCoordLoad

		b.Run(fmt.Sprintf("old_connScoreSelect/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				scores := acquireFloats(len(conns))
				_ = connScoreSelect(conns, nil, nil, &shardCostForWrites, poolWrite, true, scores.Slice(), nil, nil, 0, nil)
				scores.Release()
			}
		})
		b.Run(fmt.Sprintf("new_selectNoIndex/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = selectNoIndex(conns, &shardCostForWrites, poolWrite, true, nil, &cluster, nil)
			}
		})
	}
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"fmt"
	"net/url"
	"testing"
)

// benchShardConn is a connection the transport may hand a request to, built
// without a *testing.T so that a benchmark can use it.
func benchShardConn(name string) *Connection {
	u := &url.URL{Scheme: "http", Host: name + ":9200"}
	c := &Connection{
		URL:       u,
		URLString: u.String(),
		ID:        name,
		Name:      name,
		rttRing:   newRTTRing(4),
	}
	c.setLifecycleBit(lcReady | lcActive | lcViable)

	return c
}

// BenchmarkLookupShard measures Transport.LookupShard, which a BulkIndexer calls once per
// added item, against pools of 3, 30 and 300 nodes. The pool scans its ready list for the
// primary's name, so "last" is the worst case. It must not allocate.
func BenchmarkLookupShard(b *testing.B) {
	for _, nodes := range []int{3, 30, 300} {
		for _, where := range []string{"first", "last"} {
			b.Run(fmt.Sprintf("nodes=%d/primary=%s", nodes, where), func(b *testing.B) {
				conns := make([]*Connection, nodes)
				for i := range conns {
					conns[i] = benchShardConn(fmt.Sprintf("node-%03d", i))
				}

				primary := conns[0].Name
				if where == "last" {
					primary = conns[nodes-1].Name
				}

				tr, r := shardMapTransport(conns...)
				r.cache.getOrCreate("idx").shardMap.Store(&indexShardMap{
					NumberOfPrimaryShards: 1,
					RoutingNumShards:      1,
					Shards:                map[int]*shardNodes{0: {Primary: primary}},
				})

				b.ReportAllocs()

				var found int
				for b.Loop() {
					target, ok := tr.LookupShard("idx", "doc-1")
					if ok && target.Node != nil {
						found++
					}
				}

				if found != b.N {
					b.Fatalf("found the primary %d times in %d lookups", found, b.N)
				}
			})
		}
	}
}

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport //nolint:testpackage // Benchmarks access unexported recordAdaptiveSelection.

import (
	"strconv"
	"testing"
)

// BenchmarkRecordAdaptiveSelection records one polled node's report, an entry
// for every node in the cluster, as a poll cycle does once per node. Each
// entry changes every iteration, so every target takes a fresh report. The
// cost per report grows linearly with the cluster size.
func BenchmarkRecordAdaptiveSelection(b *testing.B) {
	for _, nodes := range []int{10, 100, 500} {
		b.Run(strconv.Itoa(nodes)+"_nodes", func(b *testing.B) {
			conns := make([]*Connection, nodes)
			report := make(map[string]AdaptiveSelectionStats, nodes)
			for i := range conns {
				id := "n" + strconv.Itoa(i)
				conns[i] = &Connection{ID: id}
				report[id] = AdaptiveSelectionStats{}
			}
			peers := newConnsByID(conns)

			b.ReportAllocs()
			var tick int64
			for b.Loop() {
				tick++
				for id := range report {
					report[id] = AdaptiveSelectionStats{OutgoingSearches: tick}
				}
				recordAdaptiveSelection(conns[0], peers, "n0", report)
			}
		})
	}
}

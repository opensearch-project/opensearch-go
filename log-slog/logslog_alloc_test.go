// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !race

// The race detector instruments allocation, so testing.AllocsPerRun reports counts
// that describe the instrumentation rather than the adapter. Every CI test job runs
// with -race; `make test-alloc` runs this file without it.

package logslog_test

import (
	"testing"
)

// TestAdapterAllocations fails when a field method allocates more than
// [eventFields] allows. The benchmarks report the same counts, but only for whoever
// runs them; this is what makes a regression break the build.
//
// The rows that matter are the ones bounded at 0, which is the adapter's own
// guarantee. The nonzero ones bound slog's handlers, and are ceilings so that a Go
// release which allocates less does not fail here.
func TestAdapterAllocations(t *testing.T) {
	for _, handler := range benchHandlers() {
		for _, f := range eventFields() {
			t.Run(handler.name+"/"+f.name, func(t *testing.T) {
				want := handler.allocs(f)
				// AllocsPerRun warms up f once before measuring, so the pooled
				// event is already in hand by the first counted run.
				if got := int(testing.AllocsPerRun(100, func() { f.emit(handler.dl) })); got > want {
					t.Errorf("allocations = %d, want at most %d", got, want)
				}
			})
		}
	}
}

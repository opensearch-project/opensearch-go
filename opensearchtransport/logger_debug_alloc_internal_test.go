// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !race

// The race detector instruments allocation, so testing.AllocsPerRun reports counts
// that describe the instrumentation rather than the logger. Every CI test job runs
// with -race; `make test-alloc` runs this file without it.

package opensearchtransport

import (
	"io"
	"testing"
)

// TestTextDebugLoggerAllocations fails when a field method allocates more than
// [eventFields] allows. The benchmarks report the same counts, but only for whoever
// runs them; this is what makes a regression break the build.
func TestTextDebugLoggerAllocations(t *testing.T) {
	dl := &textDebugLogger{Output: io.Discard}

	for _, f := range eventFields() {
		t.Run(f.name, func(t *testing.T) {
			// AllocsPerRun warms up f once before measuring, so the pooled event
			// and its buffer are already in hand by the first counted run.
			if got := int(testing.AllocsPerRun(100, func() { f.emit(dl) })); got > f.maxAllocs {
				t.Errorf("allocations = %d, want at most %d", got, f.maxAllocs)
			}
		})
	}
}

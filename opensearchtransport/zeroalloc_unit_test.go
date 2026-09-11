// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// TestClassify_ZeroAlloc guards the zero-allocation claim documented in
// CHANGELOG: OperationClassifier.Classify must not allocate on the hot
// path (it lives inside RoundTrip and runs once per request). A
// regression here means a per-request heap object that compounds across
// the cluster's RPS.
//
// testing.AllocsPerRun is a process-wide allocation differential, so it is
// only sound in a binary where nothing else allocates concurrently with the
// measured closure. The !integration constraint on this file is what
// guarantees that: it keeps the assertion out of every binary that runs the
// live-cluster tests, whose transports poll node stats and cluster health in
// the background.
func TestClassify_ZeroAlloc(t *testing.T) {
	c := opensearchtransport.NewOperationClassifier()
	// Warm any one-time setup the classifier may do.
	_ = c.Classify(http.MethodGet, "/events/_search")

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"search hot path", http.MethodPost, "/events/_search"},
		{"bulk hot path", http.MethodPost, "/_bulk"},
		{"doc get hot path", http.MethodGet, "/events/_doc/abc-123"},
		{"unknown path falls through to OpOther", http.MethodGet, "/_unknown/endpoint"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allocs := testing.AllocsPerRun(200, func() {
				_ = c.Classify(tt.method, tt.path)
			})
			require.Zero(t, allocs, "Classify(%q, %q) must be zero-alloc, got %g", tt.method, tt.path, allocs)
		})
	}
}

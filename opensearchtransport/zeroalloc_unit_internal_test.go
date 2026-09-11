// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNewRequestEventZeroAlloc guards that building the RequestEvent snapshot
// fired at observers allocates nothing: all fields are scalars or strings copied
// from the pre-captured streamResult (Host from the connection's cached
// hostPort), so the value stays on the stack.
//
// testing.AllocsPerRun is a process-wide allocation differential, so it is
// only sound in a binary where nothing else allocates concurrently with the
// measured closure. The !integration constraint on this file is what
// guarantees that: it keeps the assertion out of every binary that runs the
// live-cluster tests, whose transports poll node stats and cluster health in
// the background.
func TestNewRequestEventZeroAlloc(t *testing.T) {
	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Scheme: "http", Host: "node-1:9200", Path: "/idx/_search"},
	}
	sr := streamResult{
		escapedPath: "/idx/_search",
		routeName:   "search",
		index:       "idx",
		poolName:    "search",
		hostPort:    "http://node-1:9200",
	}
	allocs := testing.AllocsPerRun(100, func() {
		ev := newRequestEvent(req, sr)
		_ = ev
	})
	require.Zero(t, allocs, "newRequestEvent must not allocate")
}

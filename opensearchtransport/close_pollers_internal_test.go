// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Bounds for polling the goroutine dump. The window is generous because a
// cancelled ticker goroutine only unwinds on its next scheduling slice, and CI
// CPU shares make that granularity coarse.
const (
	pollerStackTimeout = 2 * time.Second
	pollerStackTick    = 20 * time.Millisecond
)

// goroutineDumpHas reports whether any live goroutine's stack mentions frame.
// The buffer grows until the dump fits, because runtime.Stack silently
// truncates and a truncated dump would report a live goroutine as gone.
func goroutineDumpHas(frame string) bool {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return strings.Contains(string(buf[:n]), frame)
		}
		buf = make([]byte, 2*len(buf))
	}
}

// TestCloseReapsBackgroundPollers guards the goroutine contract every test in
// this package relies on. New starts the node-stats and cluster-health pollers
// unconditionally -- both rates are always derived to a positive value -- and
// Close is the only thing that stops them. A test that constructs a Transport
// without releasing it therefore leaves live tickers running for the remaining
// life of the test binary, where they perturb process-wide measurements. If
// Close stops reaping these goroutines, this test fails directly instead of
// surfacing as an unrelated flake.
//
// Deliberately not parallel: it reads the process-wide goroutine dump, which is
// only quiet while no parallel sibling is running.
func TestCloseReapsBackgroundPollers(t *testing.T) {
	pollers := []struct {
		name  string
		frame string
	}{
		{"node stats poller", "scheduleNodeStats"},
		{"cluster health refresh", "scheduleClusterHealthRefresh"},
	}

	tp, err := New(Config{URLs: []*url.URL{{Scheme: "http", Host: "localhost:9200"}}})
	require.NoError(t, err)
	// Close is the operation under test; it is also registered as cleanup so a
	// failing assertion still reclaims the pollers. Close is idempotent: it
	// cancels a context and closes idle connections.
	t.Cleanup(func() { _ = tp.Close() })

	for _, p := range pollers {
		require.Eventually(t, func() bool { return goroutineDumpHas(p.frame) },
			pollerStackTimeout, pollerStackTick,
			"New must start the %s goroutine (%s)", p.name, p.frame)
	}

	require.NoError(t, tp.Close())

	for _, p := range pollers {
		require.Eventually(t, func() bool { return !goroutineDumpHas(p.frame) },
			pollerStackTimeout, pollerStackTick,
			"Close must reap the %s goroutine (%s)", p.name, p.frame)
	}
}

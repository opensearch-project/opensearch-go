// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v5/debuglog"
)

// eventField is one [debuglog.Event] method, the record a call to it produces,
// and the most allocations that record may cost.
//
// maxAllocs is asserted by TestTextDebugLoggerAllocations as a ceiling rather than
// an equality, so a future Go release that allocates less does not fail the test
// while a regression still does. Every entry here is 0 but Stringer, so for this
// implementation the ceiling is also the exact count.
type eventField struct {
	name      string
	maxAllocs int
	emit      func(debuglog.Logger)
}

// resolvedStringer is a fmt.Stringer whose String returns a string it already
// holds. It separates what [debuglog.Event.Stringer] costs from what the value
// handed to it costs to render.
type resolvedStringer string

// String implements fmt.Stringer.
func (s resolvedStringer) String() string { return string(s) }

// eventFields returns one entry per [debuglog.Event] field method.
//
// Every method is allocation-free but Stringer, which calls String on the value it
// was handed. The allocation is that call's, not the logger's: the entry below
// passes a *url.URL, whose String builds a new string each time, while the
// "Stringer, resolved" entry passes a String that returns a string it already holds
// and costs nothing.
//
// This table is duplicated in the log-zerolog and log-slog benchmarks so the three
// sets of numbers describe the same work. Changing an entry here means changing it
// in all three.
func eventFields() []eventField {
	var (
		connURL  = &url.URL{Scheme: "https", Host: "localhost:9200"}
		connText = connURL.String()
		indices  = []string{"idx-a", "idx-b", "idx-c"}
		stamp    = time.Unix(1700000000, 123456789)
		errConn  = errors.New("connection refused")
	)

	// Held as fmt.Stringer, not as resolvedStringer, so the conversion to the
	// interface happens once here rather than on every call. Boxing a string
	// allocates, and boxing it inside the closure would charge Stringer for it.
	// A *url.URL needs no such care: a pointer fits in the interface's data word.
	var resolved fmt.Stringer = resolvedStringer(connText)

	return []eventField{
		{"no fields", 0, func(dl debuglog.Logger) { dl.Debug().Msg("bench") }},
		{"Str", 0, func(dl debuglog.Logger) { dl.Debug().Str("k", connText).Msg("bench") }},
		{"Strs", 0, func(dl debuglog.Logger) { dl.Debug().Strs("k", indices).Msg("bench") }},
		{"Int", 0, func(dl debuglog.Logger) { dl.Debug().Int("k", 3).Msg("bench") }},
		{"Int32", 0, func(dl debuglog.Logger) { dl.Debug().Int32("k", 8).Msg("bench") }},
		{"Int64", 0, func(dl debuglog.Logger) { dl.Debug().Int64("k", 3).Msg("bench") }},
		{"Uint32", 0, func(dl debuglog.Logger) { dl.Debug().Uint32("k", 7).Msg("bench") }},
		{"Float64", 0, func(dl debuglog.Logger) { dl.Debug().Float64("k", 0.85).Msg("bench") }},
		{"Dur", 0, func(dl debuglog.Logger) { dl.Debug().Dur("k", 1500*time.Millisecond).Msg("bench") }},
		{"Time", 0, func(dl debuglog.Logger) { dl.Debug().Time("k", stamp).Msg("bench") }},
		{"Err", 0, func(dl debuglog.Logger) { dl.Debug().Err(errConn).Msg("bench") }},
		{"Stringer", 1, func(dl debuglog.Logger) { dl.Debug().Stringer("k", connURL).Msg("bench") }},
		{"Stringer, resolved", 0, func(dl debuglog.Logger) { dl.Debug().Stringer("k", resolved).Msg("bench") }},
	}
}

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

// BenchmarkTextDebugLoggerFields measures each field method against the built-in
// logger, the one OPENSEARCH_GO_DEBUG and Config.EnableDebugLogger install. It is
// the baseline an adapter has to be worth switching away from.
//
// One field per row is the whole benchmark. Fields append into a pooled byte
// buffer, so a record's cost is the "no fields" row plus one row per field it
// carries; a record of eight fields measures nothing a record of one did not.
func BenchmarkTextDebugLoggerFields(b *testing.B) {
	dl := &textDebugLogger{Output: io.Discard}

	for _, f := range eventFields() {
		b.Run(f.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				f.emit(dl)
			}
		})
	}
}

// benchWidthKeys are distinct keys for the record-width benchmarks, so the rows
// differ only in how many fields the record carries.
var benchWidthKeys = []string{"k0", "k1", "k2", "k3", "k4", "k5", "k6", "k7"}

// benchWidths brackets log-slog's five-attribute inline limit, so a comparison
// across the three implementations shows where that cost appears and where it
// does not.
var benchWidths = []int{4, 5, 6, 8}

// BenchmarkTextDebugLoggerRecordWidth measures how a record's cost scales with the
// number of fields it carries. Every field is a Str, so no per-field difference is
// mixed in. The same benchmark exists in log-zerolog and log-slog.
func BenchmarkTextDebugLoggerRecordWidth(b *testing.B) {
	dl := &textDebugLogger{Output: io.Discard}

	for _, n := range benchWidths {
		b.Run(fmt.Sprintf("%d fields", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				event := dl.Debug()
				for i := range n {
					event = event.Str(benchWidthKeys[i], "search")
				}
				event.Msg("bench")
			}
		})
	}
}

// BenchmarkDebugNoLoggerInstalled measures what the client's own emitting sites
// cost with debug logging off, through the real accessor rather than through
// debuglog.Nop directly, so the atomic load is included. This is the number that
// matters for the 85 unguarded sites: it is paid on every request path that
// touches one.
//
// The record carries a Stringer, the one field that allocates when a logger is
// installed. Nothing is emitted here, so String is never called and the field
// costs nothing, which is the reason Stringer is in the interface at all.
func BenchmarkDebugNoLoggerInstalled(b *testing.B) {
	connURL := &url.URL{Scheme: "https", Host: "localhost:9200"}
	errConn := errors.New("connection refused")

	previous := debugLoggerPtr.Load()
	storeDebugLogger(nil)
	b.Cleanup(func() {
		if previous != nil {
			storeDebugLogger(*previous)
		}
	})

	b.ReportAllocs()
	for b.Loop() {
		Debug().
			Stringer("conn", connURL).
			Int("attempts", 3).
			Dur("took", 1500*time.Millisecond).
			Err(errConn).
			Msg("bench")
	}
}

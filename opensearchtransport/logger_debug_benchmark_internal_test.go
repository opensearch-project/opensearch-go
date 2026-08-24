// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"errors"
	"io"
	"net/url"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v5/debuglog"
)

// The record shapes here are identical to those in the log-zerolog and log-slog
// benchmarks, so the three sets of numbers are comparable. Changing a shape here
// means changing it in all three.
var (
	benchDebugURL  = &url.URL{Scheme: "https", Host: "localhost:9200"}
	errBenchDebug  = errors.New("connection refused")
	benchConnText  = benchDebugURL.String()
	benchDebugStrs = []string{"idx-a", "idx-b", "idx-c"}
	benchDebugTime = time.Unix(1700000000, 123456789)
)

// debugFieldBenchmarks isolates what one [debuglog.Event] method costs. Every
// row emits a one-field record, so subtracting the "no fields" row leaves the
// field's own cost: each row also pays Debug, Msg, and the indirect call through
// this table, and those are the same for every row.
//
// The values match the ones the eight-field shape carries, so a per-field number
// and a whole-record number describe the same work. This table is duplicated in
// the log-zerolog and log-slog benchmarks for the same reason the shapes are.
var debugFieldBenchmarks = []struct {
	name  string
	field func(debuglog.Event) debuglog.Event
}{
	{"no fields", func(e debuglog.Event) debuglog.Event { return e }},
	{"Str", func(e debuglog.Event) debuglog.Event { return e.Str("k", "search") }},
	{"Strs", func(e debuglog.Event) debuglog.Event { return e.Strs("k", benchDebugStrs) }},
	{"Int", func(e debuglog.Event) debuglog.Event { return e.Int("k", 3) }},
	{"Int32", func(e debuglog.Event) debuglog.Event { return e.Int32("k", 8) }},
	{"Int64", func(e debuglog.Event) debuglog.Event { return e.Int64("k", 3) }},
	{"Uint32", func(e debuglog.Event) debuglog.Event { return e.Uint32("k", 7) }},
	{"Float64", func(e debuglog.Event) debuglog.Event { return e.Float64("k", 0.85) }},
	{"Dur", func(e debuglog.Event) debuglog.Event { return e.Dur("k", 1500*time.Millisecond) }},
	{"Time", func(e debuglog.Event) debuglog.Event { return e.Time("k", benchDebugTime) }},
	{"Stringer", func(e debuglog.Event) debuglog.Event { return e.Stringer("k", benchDebugURL) }},
	{"Err", func(e debuglog.Event) debuglog.Event { return e.Err(errBenchDebug) }},
}

func emitOneDebugField(dl debuglog.Logger) {
	dl.Debug().Stringer("conn", benchDebugURL).Msg("Request failed")
}

// emitFourDebugFieldsNoStringer is the four-field shape with the deferred Stringer swapped for an
// already-resolved string. Every other shape here pays one allocation inside
// (*url.URL).String, so this is the shape that shows what the logger itself
// allocates, as opposed to what resolving the connection address costs.
func emitFourDebugFieldsNoStringer(dl debuglog.Logger) {
	dl.Debug().
		Str("conn", benchConnText).
		Int("attempts", 3).
		Dur("took", 1500*time.Millisecond).
		Err(errBenchDebug).
		Msg("Request failed")
}

func emitFourDebugFields(dl debuglog.Logger) {
	dl.Debug().
		Stringer("conn", benchDebugURL).
		Int("attempts", 3).
		Dur("took", 1500*time.Millisecond).
		Err(errBenchDebug).
		Msg("Request failed")
}

func emitEightDebugFields(dl debuglog.Logger) {
	dl.Debug().
		Stringer("conn", benchDebugURL).
		Int("attempts", 3).
		Dur("took", 1500*time.Millisecond).
		Err(errBenchDebug).
		Str("pool", "search").
		Int64("tripped", 3).
		Float64("ratio", 0.85).
		Int32("cwnd", 8).
		Msg("Request failed")
}

// BenchmarkTextDebugLogger measures the built-in logger, the one
// OPENSEARCH_GO_DEBUG and Config.EnableDebugLogger install. It is the baseline an
// adapter has to be worth switching away from.
func BenchmarkTextDebugLogger(b *testing.B) {
	dl := &textDebugLogger{Output: io.Discard}

	shapes := []struct {
		name string
		emit func(debuglog.Logger)
	}{
		{"one field", emitOneDebugField},
		{"four fields", emitFourDebugFields},
		{"eight fields", emitEightDebugFields},
		{"four fields, no Stringer", emitFourDebugFieldsNoStringer},
	}

	for _, shape := range shapes {
		b.Run(shape.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				shape.emit(dl)
			}
		})
	}
}

// BenchmarkTextDebugLoggerFields measures each field method on its own, so a
// caller can see what a given field adds rather than only what a whole record
// costs. Only Stringer and Time were ever suspected of allocating; this is what
// settles it for the other nine.
func BenchmarkTextDebugLoggerFields(b *testing.B) {
	dl := &textDebugLogger{Output: io.Discard}

	for _, f := range debugFieldBenchmarks {
		b.Run(f.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				f.field(dl.Debug()).Msg("Request failed")
			}
		})
	}
}

// BenchmarkDebugNoLoggerInstalled measures what the client's own emitting sites
// cost with debug logging off, through the real accessor rather than through
// debuglog.Nop directly, so the atomic load is included. This is the number that
// matters for the 85 unguarded sites: it is paid on every request path that
// touches one.
func BenchmarkDebugNoLoggerInstalled(b *testing.B) {
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
			Stringer("conn", benchDebugURL).
			Int("attempts", 3).
			Dur("took", 1500*time.Millisecond).
			Err(errBenchDebug).
			Msg("Request failed")
	}
}

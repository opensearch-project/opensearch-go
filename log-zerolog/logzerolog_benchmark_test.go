// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package logzerolog_test

import (
	"errors"
	"io"
	"net/url"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/opensearch-project/opensearch-go/v5/debuglog"
	logzerolog "github.com/opensearch-project/opensearch-go/v5/log-zerolog"
)

// The record shapes here are identical to those in log-slog's benchmark and in
// the built-in logger's, so the three sets of numbers are comparable. Changing a
// shape here means changing it in all three.
var (
	benchURL      = &url.URL{Scheme: "https", Host: "localhost:9200"}
	errBench      = errors.New("connection refused")
	benchConnText = benchURL.String()
	benchStrs     = []string{"idx-a", "idx-b", "idx-c"}
	benchTime     = time.Unix(1700000000, 123456789)
)

// fieldBenchmarks isolates what one [debuglog.Event] method costs. Every row
// emits a one-field record, so subtracting the "no fields" row leaves the field's
// own cost: each row also pays Debug, Msg, and the indirect call through this
// table, and those are the same for every row.
//
// The values match the ones the eight-field shape carries, so a per-field number
// and a whole-record number describe the same work. This table is duplicated in
// the log-slog and built-in logger benchmarks for the same reason the shapes are.
var fieldBenchmarks = []struct {
	name  string
	field func(debuglog.Event) debuglog.Event
}{
	{"no fields", func(e debuglog.Event) debuglog.Event { return e }},
	{"Str", func(e debuglog.Event) debuglog.Event { return e.Str("k", "search") }},
	{"Strs", func(e debuglog.Event) debuglog.Event { return e.Strs("k", benchStrs) }},
	{"Int", func(e debuglog.Event) debuglog.Event { return e.Int("k", 3) }},
	{"Int32", func(e debuglog.Event) debuglog.Event { return e.Int32("k", 8) }},
	{"Int64", func(e debuglog.Event) debuglog.Event { return e.Int64("k", 3) }},
	{"Uint32", func(e debuglog.Event) debuglog.Event { return e.Uint32("k", 7) }},
	{"Float64", func(e debuglog.Event) debuglog.Event { return e.Float64("k", 0.85) }},
	{"Dur", func(e debuglog.Event) debuglog.Event { return e.Dur("k", 1500*time.Millisecond) }},
	{"Time", func(e debuglog.Event) debuglog.Event { return e.Time("k", benchTime) }},
	{"Stringer", func(e debuglog.Event) debuglog.Event { return e.Stringer("k", benchURL) }},
	{"Err", func(e debuglog.Event) debuglog.Event { return e.Err(errBench) }},
}

func oneField(dl debuglog.Logger) {
	dl.Debug().Stringer("conn", benchURL).Msg("Request failed")
}

// oneFieldNoStringer is the shape the client's own one-field records have: the
// connection address arrives already resolved, from Connection.URLString. It is
// the counterpart to oneField, whose single allocation is the deferred Stringer
// rather than anything a one-field record inherently costs.
func oneFieldNoStringer(dl debuglog.Logger) {
	dl.Debug().Str("conn", benchConnText).Msg("Request failed")
}

// fourFieldsNoStringer is the four-field shape with the deferred Stringer swapped for an
// already-resolved string. Every other shape here pays one allocation inside
// (*url.URL).String, so this is the shape that shows what the logger itself
// allocates, as opposed to what resolving the connection address costs.
func fourFieldsNoStringer(dl debuglog.Logger) {
	dl.Debug().
		Str("conn", benchConnText).
		Int("attempts", 3).
		Dur("took", 1500*time.Millisecond).
		Err(errBench).
		Msg("Request failed")
}

func fourFields(dl debuglog.Logger) {
	dl.Debug().
		Stringer("conn", benchURL).
		Int("attempts", 3).
		Dur("took", 1500*time.Millisecond).
		Err(errBench).
		Msg("Request failed")
}

func eightFields(dl debuglog.Logger) {
	dl.Debug().
		Stringer("conn", benchURL).
		Int("attempts", 3).
		Dur("took", 1500*time.Millisecond).
		Err(errBench).
		Str("pool", "search").
		Int64("tripped", 3).
		Float64("ratio", 0.85).
		Int32("cwnd", 8).
		Msg("Request failed")
}

// BenchmarkAdapter measures the adapter with records actually being written.
// Output goes to io.Discard so the numbers reflect encoding rather than the
// writer.
func BenchmarkAdapter(b *testing.B) {
	dl := logzerolog.New(zerolog.New(io.Discard))

	shapes := []struct {
		name string
		emit func(debuglog.Logger)
	}{
		{"one field", oneField},
		{"one field, no Stringer", oneFieldNoStringer},
		{"four fields", fourFields},
		{"eight fields", eightFields},
		{"four fields, no Stringer", fourFieldsNoStringer},
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

// BenchmarkAdapterFields measures each field method on its own, so a caller can
// see what a given field adds rather than only what a whole record costs.
func BenchmarkAdapterFields(b *testing.B) {
	dl := logzerolog.New(zerolog.New(io.Discard))

	for _, f := range fieldBenchmarks {
		b.Run(f.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				f.field(dl.Debug()).Msg("Request failed")
			}
		})
	}
}

// BenchmarkAdapterLevelDisabled measures a record the logger's own level filter
// rejects. zerolog returns a nil event, which the adapter turns into
// debuglog.Nop, so no record is encoded and no event leaves zerolog's pool.
func BenchmarkAdapterLevelDisabled(b *testing.B) {
	dl := logzerolog.New(zerolog.New(io.Discard).Level(zerolog.InfoLevel))

	b.ReportAllocs()
	for b.Loop() {
		fourFields(dl)
	}
}

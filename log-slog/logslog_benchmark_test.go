// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package logslog_test

import (
	"errors"
	"io"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v5/debuglog"
	logslog "github.com/opensearch-project/opensearch-go/v5/log-slog"
)

// The record shapes here are identical to those in log-zerolog's benchmark and in
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
// the log-zerolog and built-in logger benchmarks for the same reason the shapes
// are.
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

func debugHandlerLogger(h func(io.Writer, *slog.HandlerOptions) slog.Handler) debuglog.Logger {
	return logslog.New(slog.New(h(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

// BenchmarkAdapter measures the adapter with records actually being written.
//
// Both handlers are measured because the choice dominates the result: JSONHandler
// is the like-for-like comparison against zerolog, which only writes JSON, while
// TextHandler is what slog gives you by default.
func BenchmarkAdapter(b *testing.B) {
	handlers := []struct {
		name string
		dl   debuglog.Logger
	}{
		{"json", debugHandlerLogger(func(w io.Writer, o *slog.HandlerOptions) slog.Handler {
			return slog.NewJSONHandler(w, o)
		})},
		{"text", debugHandlerLogger(func(w io.Writer, o *slog.HandlerOptions) slog.Handler {
			return slog.NewTextHandler(w, o)
		})},
	}

	shapes := []struct {
		name string
		emit func(debuglog.Logger)
	}{
		{"one field", oneField},
		{"four fields", fourFields},
		{"eight fields", eightFields},
		{"four fields, no Stringer", fourFieldsNoStringer},
	}

	for _, handler := range handlers {
		for _, shape := range shapes {
			b.Run(handler.name+"/"+shape.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					shape.emit(handler.dl)
				}
			})
		}
	}
}

// BenchmarkAdapterFields measures each field method on its own, so a caller can
// see what a given field adds rather than only what a whole record costs. Both
// handlers run, because which fields are dear differs between them: JSONHandler
// marshals a float64 through encoding/json, and TextHandler renders an error
// through fmt.
func BenchmarkAdapterFields(b *testing.B) {
	handlers := []struct {
		name string
		dl   debuglog.Logger
	}{
		{"json", debugHandlerLogger(func(w io.Writer, o *slog.HandlerOptions) slog.Handler {
			return slog.NewJSONHandler(w, o)
		})},
		{"text", debugHandlerLogger(func(w io.Writer, o *slog.HandlerOptions) slog.Handler {
			return slog.NewTextHandler(w, o)
		})},
	}

	for _, handler := range handlers {
		for _, f := range fieldBenchmarks {
			b.Run(handler.name+"/"+f.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					f.field(handler.dl.Debug()).Msg("Request failed")
				}
			})
		}
	}
}

// BenchmarkAdapterLevelDisabled measures a record the handler's level rejects.
// The adapter checks Enabled in Debug and returns debuglog.Nop, so no attributes
// are accumulated and no caller frame is resolved.
//
// Both handlers are measured even though the handler never runs, so the table in
// the README reports two measured numbers rather than one measured and one
// assumed to match.
func BenchmarkAdapterLevelDisabled(b *testing.B) {
	handlers := []struct {
		name       string
		newHandler func(io.Writer, *slog.HandlerOptions) slog.Handler
	}{
		{"json", func(w io.Writer, o *slog.HandlerOptions) slog.Handler { return slog.NewJSONHandler(w, o) }},
		{"text", func(w io.Writer, o *slog.HandlerOptions) slog.Handler { return slog.NewTextHandler(w, o) }},
	}

	for _, handler := range handlers {
		dl := logslog.New(slog.New(handler.newHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo})))
		b.Run(handler.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				fourFields(dl)
			}
		})
	}
}

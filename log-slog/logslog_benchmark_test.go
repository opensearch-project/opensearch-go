// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package logslog_test

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v5/debuglog"
	logslog "github.com/opensearch-project/opensearch-go/v5/log-slog"
)

// eventField is one [debuglog.Event] method, the record a call to it produces, and
// the most allocations that record may cost under each of slog's two handlers.
//
// The counts are asserted by TestAdapterAllocations as ceilings rather than
// equalities, which matters more here than in the other two implementations: the
// nonzero entries are slog's handlers rather than this adapter, and they move
// between Go releases. Strs and Float64 under JSONHandler each cost one allocation
// on go1.26 and two on go1.27. A ceiling lets the adapter's own guarantee, the rows
// that are 0, stay strict without a toolchain bump turning the rest red.
//
// Two columns because the handler is what allocates: JSONHandler routes a []string
// and a float64 through encoding/json, while TextHandler renders a []string and an
// error through fmt.
type eventField struct {
	name          string
	maxJSONAllocs int
	maxTextAllocs int
	emit          func(debuglog.Logger)
}

// resolvedStringer is a fmt.Stringer whose String returns a string it already
// holds. It separates what [debuglog.Event.Stringer] costs from what the value
// handed to it costs to render.
type resolvedStringer string

// String implements fmt.Stringer.
func (s resolvedStringer) String() string { return string(s) }

// eventFields returns one entry per [debuglog.Event] field method.
//
// Stringer allocates under every handler because it calls String on the value it was
// handed. The allocation is that call's, not the adapter's: the entry below passes a
// *url.URL, whose String builds a new string each time, while the "Stringer,
// resolved" entry passes a String that returns a string it already holds and costs
// nothing.
//
// This table is duplicated in the log-zerolog and built-in logger benchmarks so the
// three sets of numbers describe the same work. Changing an entry here means
// changing it in all three.
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
		{"no fields", 0, 0, func(dl debuglog.Logger) { dl.Debug().Msg("bench") }},
		{"Str", 0, 0, func(dl debuglog.Logger) { dl.Debug().Str("k", connText).Msg("bench") }},
		{"Strs", 3, 5, func(dl debuglog.Logger) { dl.Debug().Strs("k", indices).Msg("bench") }},
		{"Int", 0, 0, func(dl debuglog.Logger) { dl.Debug().Int("k", 3).Msg("bench") }},
		{"Int32", 0, 0, func(dl debuglog.Logger) { dl.Debug().Int32("k", 8).Msg("bench") }},
		{"Int64", 0, 0, func(dl debuglog.Logger) { dl.Debug().Int64("k", 3).Msg("bench") }},
		{"Uint32", 0, 0, func(dl debuglog.Logger) { dl.Debug().Uint32("k", 7).Msg("bench") }},
		{"Float64", 3, 0, func(dl debuglog.Logger) { dl.Debug().Float64("k", 0.85).Msg("bench") }},
		{"Dur", 0, 0, func(dl debuglog.Logger) { dl.Debug().Dur("k", 1500*time.Millisecond).Msg("bench") }},
		{"Time", 0, 0, func(dl debuglog.Logger) { dl.Debug().Time("k", stamp).Msg("bench") }},
		{"Err", 0, 1, func(dl debuglog.Logger) { dl.Debug().Err(errConn).Msg("bench") }},
		{"Stringer", 1, 1, func(dl debuglog.Logger) { dl.Debug().Stringer("k", connURL).Msg("bench") }},
		{"Stringer, resolved", 0, 0, func(dl debuglog.Logger) { dl.Debug().Stringer("k", resolved).Msg("bench") }},
	}
}

// benchHandler pairs a handler with the column of [eventField] that bounds it.
type benchHandler struct {
	name   string
	dl     debuglog.Logger
	allocs func(eventField) int
}

// benchHandlers returns the pair of handlers every test and benchmark here runs
// against. The choice dominates the result: JSONHandler is the like-for-like
// comparison against zerolog, which only writes JSON, while TextHandler is what
// slog gives you by default.
func benchHandlers() []benchHandler {
	newLogger := func(h func(io.Writer, *slog.HandlerOptions) slog.Handler) debuglog.Logger {
		return logslog.New(slog.New(h(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}

	return []benchHandler{
		{
			name: "json",
			dl: newLogger(func(w io.Writer, o *slog.HandlerOptions) slog.Handler {
				return slog.NewJSONHandler(w, o)
			}),
			allocs: func(f eventField) int { return f.maxJSONAllocs },
		},
		{
			name: "text",
			dl: newLogger(func(w io.Writer, o *slog.HandlerOptions) slog.Handler {
				return slog.NewTextHandler(w, o)
			}),
			allocs: func(f eventField) int { return f.maxTextAllocs },
		},
	}
}

// BenchmarkAdapterFields measures each field method with records actually being
// written to io.Discard.
//
// One field per row is the whole benchmark: a record's cost is the "no fields" row
// plus one row per field it carries. The one thing that does not follow from the
// rows is the field count itself, which slog charges for past five and
// BenchmarkAdapterRecordWidth measures.
func BenchmarkAdapterFields(b *testing.B) {
	for _, handler := range benchHandlers() {
		for _, f := range eventFields() {
			b.Run(handler.name+"/"+f.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					f.emit(handler.dl)
				}
			})
		}
	}
}

// BenchmarkAdapterRecordWidth measures how a record's cost scales with the number
// of fields it carries. Every field is a Str, so no per-field difference is mixed
// in. The same benchmark exists in log-zerolog and the built-in logger.
//
// The widths bracket slog's five-attribute inline limit: slog.Record stores five
// attributes inline and moves the remainder to the heap, so the step from five to
// six is that spill and nothing else. The other two implementations stay flat
// across all four widths.
func BenchmarkAdapterRecordWidth(b *testing.B) {
	keys := []string{"k0", "k1", "k2", "k3", "k4", "k5", "k6", "k7"}

	for _, handler := range benchHandlers() {
		for _, n := range []int{4, 5, 6, 8} {
			b.Run(fmt.Sprintf("%s/%d fields", handler.name, n), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					event := handler.dl.Debug()
					for i := range n {
						event = event.Str(keys[i], "search")
					}
					event.Msg("bench")
				}
			})
		}
	}
}

// BenchmarkAdapterLevelDisabled measures a record the handler's level rejects. The
// adapter checks Enabled in Debug and returns debuglog.Nop, so no attributes are
// accumulated and no caller frame is resolved.
//
// The record carries a Stringer, the one field that allocates when the level
// admits it. Nothing is emitted here, so String is never called and the field costs
// nothing, which is the reason Stringer is in the interface at all.
//
// Both handlers are measured even though neither runs, so the README reports two
// measured numbers rather than one measured and one assumed to match.
func BenchmarkAdapterLevelDisabled(b *testing.B) {
	connURL := &url.URL{Scheme: "https", Host: "localhost:9200"}
	errConn := errors.New("connection refused")

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
				dl.Debug().
					Stringer("conn", connURL).
					Int("attempts", 3).
					Dur("took", 1500*time.Millisecond).
					Err(errConn).
					Msg("bench")
			}
		})
	}
}

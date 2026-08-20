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
	benchURL = &url.URL{Scheme: "https", Host: "localhost:9200"}
	benchErr = errors.New("connection refused")
)

func oneField(dl debuglog.Logger) {
	dl.Debug().Stringer("conn", benchURL).Msg("Request failed")
}

func fourFields(dl debuglog.Logger) {
	dl.Debug().
		Stringer("conn", benchURL).
		Int("attempts", 3).
		Dur("took", 1500*time.Millisecond).
		Err(benchErr).
		Msg("Request failed")
}

func eightFields(dl debuglog.Logger) {
	dl.Debug().
		Stringer("conn", benchURL).
		Int("attempts", 3).
		Dur("took", 1500*time.Millisecond).
		Err(benchErr).
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

// BenchmarkAdapterLevelDisabled measures a record the handler's level rejects.
// The adapter checks Enabled in Debug and returns debuglog.Nop, so no attributes
// are accumulated and no caller frame is resolved.
//
// Both handlers are measured even though the handler never runs, so the table in
// the README reports two measured numbers rather than one measured and one
// assumed to match.
func BenchmarkAdapterLevelDisabled(b *testing.B) {
	handlers := []struct {
		name string
		new  func(io.Writer, *slog.HandlerOptions) slog.Handler
	}{
		{"json", func(w io.Writer, o *slog.HandlerOptions) slog.Handler { return slog.NewJSONHandler(w, o) }},
		{"text", func(w io.Writer, o *slog.HandlerOptions) slog.Handler { return slog.NewTextHandler(w, o) }},
	}

	for _, handler := range handlers {
		dl := logslog.New(slog.New(handler.new(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo})))
		b.Run(handler.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				fourFields(dl)
			}
		})
	}
}

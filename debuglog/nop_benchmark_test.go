// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package debuglog_test

import (
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v5/debuglog"
)

// The record shapes below are duplicated verbatim in the log-zerolog and log-slog
// benchmarks, and in the built-in logger's. Comparing implementations only means
// something if they are handed identical records, so the shapes are copied rather
// than shared: a shared helper would have to live in an importable package, and
// debuglog deliberately has no test dependencies to put one in.
var (
	benchURL = &url.URL{Scheme: "https", Host: "localhost:9200"}
	errBench = errors.New("connection refused")
)

// BenchmarkNop measures the floor every emitting site pays when no logger is
// installed. The 85 sites in the client are unguarded, so this is the cost of
// debug logging being switched off.
func BenchmarkNop(b *testing.B) {
	b.Run("one field", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			debuglog.Nop().Stringer("conn", benchURL).Msg("Request failed")
		}
	})

	b.Run("four fields", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			debuglog.Nop().
				Stringer("conn", benchURL).
				Int("attempts", 3).
				Dur("took", 1500*time.Millisecond).
				Err(errBench).
				Msg("Request failed")
		}
	})

	b.Run("eight fields", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			debuglog.Nop().
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
	})
}

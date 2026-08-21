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
	benchDebugURL = &url.URL{Scheme: "https", Host: "localhost:9200"}
	errBenchDebug = errors.New("connection refused")
)

func emitOneDebugField(dl debuglog.Logger) {
	dl.Debug().Stringer("conn", benchDebugURL).Msg("Request failed")
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

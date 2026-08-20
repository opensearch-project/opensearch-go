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
		{"four fields", fourFields},
		{"eight fields", eightFields},
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

// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package logzerolog

import (
	"bytes"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/debuglog"
)

// TestEventTypedMethods pins that each typed Event method forwards to the
// matching zerolog method, including the two cases most likely to regress: a
// nil *url.URL passed to Stringer, and the field key Err uses.
func TestEventTypedMethods(t *testing.T) {
	t.Parallel()

	connURL, err := url.Parse("https://localhost:9200")
	require.NoError(t, err)

	tests := []struct {
		name  string
		build func(debuglog.Event) debuglog.Event
		want  string
	}{
		{
			name:  "Str",
			build: func(e debuglog.Event) debuglog.Event { return e.Str("conn", "node-1") },
			want:  `{"level":"debug","conn":"node-1","message":"typed field"}`,
		},
		{
			name:  "Strs",
			build: func(e debuglog.Event) debuglog.Event { return e.Strs("nodes", []string{"a", "b"}) },
			want:  `{"level":"debug","nodes":["a","b"],"message":"typed field"}`,
		},
		{
			name:  "Int",
			build: func(e debuglog.Event) debuglog.Event { return e.Int("attempts", 3) },
			want:  `{"level":"debug","attempts":3,"message":"typed field"}`,
		},
		{
			name:  "Int32",
			build: func(e debuglog.Event) debuglog.Event { return e.Int32("code", int32(7)) },
			want:  `{"level":"debug","code":7,"message":"typed field"}`,
		},
		{
			name:  "Int64",
			build: func(e debuglog.Event) debuglog.Event { return e.Int64("bytes", int64(1024)) },
			want:  `{"level":"debug","bytes":1024,"message":"typed field"}`,
		},
		{
			name:  "Uint32",
			build: func(e debuglog.Event) debuglog.Event { return e.Uint32("port", uint32(9200)) },
			want:  `{"level":"debug","port":9200,"message":"typed field"}`,
		},
		{
			name:  "Float64",
			build: func(e debuglog.Event) debuglog.Event { return e.Float64("ratio", 0.5) },
			want:  `{"level":"debug","ratio":0.5,"message":"typed field"}`,
		},
		{
			// time.Duration implements fmt.Stringer, but Dur forwards to zerolog's
			// own Dur rather than going through Stringer, so DurationFieldUnit
			// (milliseconds by default) still applies instead of "1.5s".
			name:  "Dur",
			build: func(e debuglog.Event) debuglog.Event { return e.Dur("timeout", 1500*time.Millisecond) },
			want:  `{"level":"debug","timeout":1500,"message":"typed field"}`,
		},
		{
			// time.Time also implements fmt.Stringer, but Time forwards to
			// zerolog's own Time rather than going through Stringer, so
			// TimeFieldFormat (RFC3339 by default) still applies instead of
			// time.Time.String()'s format.
			name: "Time",
			build: func(e debuglog.Event) debuglog.Event {
				return e.Time("seen", time.Date(2026, 8, 19, 4, 13, 43, 0, time.UTC))
			},
			want: `{"level":"debug","seen":"2026-08-19T04:13:43Z","message":"typed field"}`,
		},
		{
			name:  "Stringer",
			build: func(e debuglog.Event) debuglog.Event { return e.Stringer("conn", connURL) },
			want:  `{"level":"debug","conn":"https://localhost:9200","message":"typed field"}`,
		},
		{
			// A nil *url.URL satisfies fmt.Stringer, so the interface value is
			// non-nil while the pointer inside it is not. Rendering must not
			// dereference it.
			name:  "Stringer nil pointer",
			build: func(e debuglog.Event) debuglog.Event { return e.Stringer("conn", (*url.URL)(nil)) },
			want:  `{"level":"debug","conn":"<nil>","message":"typed field"}`,
		},
		{
			// Err goes through zerolog's own Err, so the key is zerolog's
			// configured ErrorFieldName ("error" by default), not the "err" the
			// built-in logger and log-slog use.
			name:  "Err",
			build: func(e debuglog.Event) debuglog.Event { return e.Err(errors.New("connection refused")) },
			want:  `{"level":"debug","error":"connection refused","message":"typed field"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			tt.build(New(zerolog.New(&buf)).Debug()).Msg("typed field")

			require.JSONEq(t, tt.want, buf.String())
		})
	}
}

// TestNewChainsMultipleFields pins that Msg emits every field accumulated
// across the chain, not just the last one.
func TestNewChainsMultipleFields(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	New(zerolog.New(&buf)).Debug().
		Str("conn", "https://localhost:9200").
		Int("heap_used_percent", 93).
		Msg("Node overloaded")

	require.JSONEq(
		t,
		`{"level":"debug","conn":"https://localhost:9200","heap_used_percent":93,"message":"Node overloaded"}`,
		buf.String(),
	)
}

// TestDebugDisabledLevel pins that a logger whose level excludes debug yields
// no output at all, and that the Event Debug returns in that case is safe to
// chain and call Msg on.
func TestDebugDisabledLevel(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	zl := zerolog.New(&buf).Level(zerolog.InfoLevel)

	require.NotPanics(t, func() {
		New(zl).Debug().
			Str("conn", "https://localhost:9200").
			Int("attempts", 3).
			Stringer("nil_conn", (*url.URL)(nil)).
			Err(errors.New("boom")).
			Msg("Node overloaded")
	})
	require.Empty(t, buf.String())
}

// TestDefaultReadsPackageLoggerPerRecord pins that Default() resolves
// log.Logger when it writes rather than capturing it, which is what its godoc
// promises. The DebugLogger is built before log.Logger is reassigned, the order
// an application produces when it builds a client config and configures logging
// in main; an adapter that captured the logger would write to the old one.
//
// Not parallel, and restores the previous logger: log.Logger is process-wide.
func TestDefaultReadsPackageLoggerPerRecord(t *testing.T) {
	previous := log.Logger
	t.Cleanup(func() { log.Logger = previous })

	debugLogger := Default()

	var buf bytes.Buffer
	log.Logger = zerolog.New(&buf)

	debugLogger.Debug().Str("conn", "https://localhost:9200").Msg("Node overloaded")

	require.JSONEq(
		t,
		`{"level":"debug","conn":"https://localhost:9200","message":"Node overloaded"}`,
		buf.String(),
	)
}

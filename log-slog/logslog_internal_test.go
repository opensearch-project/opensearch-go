// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package logslog

import (
	"bytes"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/debuglog"
)

// TestEventTypedMethods pins that each typed Event method renders its value
// through slog, including the two cases most likely to regress: a nil
// *url.URL passed to Stringer, and the field key Err uses.
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
			want:  `level=DEBUG msg="typed field" conn=node-1`,
		},
		{
			name:  "Strs",
			build: func(e debuglog.Event) debuglog.Event { return e.Strs("nodes", []string{"a", "b"}) },
			want:  `level=DEBUG msg="typed field" nodes="[a b]"`,
		},
		{
			name:  "Int",
			build: func(e debuglog.Event) debuglog.Event { return e.Int("attempts", 3) },
			want:  `level=DEBUG msg="typed field" attempts=3`,
		},
		{
			name:  "Int32",
			build: func(e debuglog.Event) debuglog.Event { return e.Int32("code", int32(7)) },
			want:  `level=DEBUG msg="typed field" code=7`,
		},
		{
			name:  "Int64",
			build: func(e debuglog.Event) debuglog.Event { return e.Int64("bytes", int64(1024)) },
			want:  `level=DEBUG msg="typed field" bytes=1024`,
		},
		{
			name:  "Uint32",
			build: func(e debuglog.Event) debuglog.Event { return e.Uint32("port", uint32(9200)) },
			want:  `level=DEBUG msg="typed field" port=9200`,
		},
		{
			name:  "Float64",
			build: func(e debuglog.Event) debuglog.Event { return e.Float64("ratio", 0.5) },
			want:  `level=DEBUG msg="typed field" ratio=0.5`,
		},
		{
			name:  "Dur",
			build: func(e debuglog.Event) debuglog.Event { return e.Dur("timeout", 1500*time.Millisecond) },
			want:  `level=DEBUG msg="typed field" timeout=1.5s`,
		},
		{
			name: "Time",
			build: func(e debuglog.Event) debuglog.Event {
				return e.Time("seen", time.Date(2026, 8, 19, 4, 13, 43, 0, time.UTC))
			},
			want: `level=DEBUG msg="typed field" seen=2026-08-19T04:13:43.000Z`,
		},
		{
			name:  "Stringer",
			build: func(e debuglog.Event) debuglog.Event { return e.Stringer("conn", connURL) },
			want:  `level=DEBUG msg="typed field" conn=https://localhost:9200`,
		},
		{
			// A nil *url.URL satisfies fmt.Stringer, so the interface value is
			// non-nil while the pointer inside it is not. Rendering must not
			// dereference it.
			name:  "Stringer nil pointer",
			build: func(e debuglog.Event) debuglog.Event { return e.Stringer("conn", (*url.URL)(nil)) },
			want:  `level=DEBUG msg="typed field" conn=<nil>`,
		},
		{
			// slog's own key for an error field is "err", independent of the
			// built-in logger's key. log-zerolog uses zerolog's ErrorFieldName
			// instead.
			name:  "Err",
			build: func(e debuglog.Event) debuglog.Event { return e.Err(errors.New("connection refused")) },
			want:  `level=DEBUG msg="typed field" err="connection refused"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
				Level: slog.LevelDebug,
				ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
					if a.Key == slog.TimeKey {
						return slog.Attr{}
					}
					return a
				},
			})

			tt.build(New(slog.New(handler)).Debug()).Msg("typed field")

			require.Equal(t, tt.want, strings.TrimSpace(buf.String()))
		})
	}
}

// TestNewChainsMultipleFields pins that Msg emits every field accumulated
// across the chain, not just the last one.
func TestNewChainsMultipleFields(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})

	New(slog.New(handler)).Debug().
		Str("conn", "https://localhost:9200").
		Int("heap_used_percent", 93).
		Msg("Node overloaded")

	require.Equal(
		t,
		`level=DEBUG msg="Node overloaded" conn=https://localhost:9200 heap_used_percent=93`,
		strings.TrimSpace(buf.String()),
	)
}

// TestReusesEvents pins the one hazard pooling introduces: an event handed back
// by Msg and picked up again by the next Debug must carry none of the previous
// record's attributes.
//
// The records differ in attribute count so that a stale slice shows up either way
// round, whichever of them the pool happens to serve first.
func TestReusesEvents(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	dl := New(slog.New(handler))

	dl.Debug().Str("conn", "node-1").Int("attempts", 2).Msg("first")
	dl.Debug().Str("conn", "node-2").Msg("second")
	dl.Debug().Msg("third")

	require.Equal(t, []string{
		`level=DEBUG msg=first conn=node-1 attempts=2`,
		`level=DEBUG msg=second conn=node-2`,
		`level=DEBUG msg=third`,
	}, strings.Split(strings.TrimSpace(buf.String()), "\n"))
}

// TestDebugDisabledLevel pins that a handler which does not admit
// slog.LevelDebug yields no output at all, and that the Event Debug returns
// in that case is safe to chain and call Msg on.
func TestDebugDisabledLevel(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	require.NotPanics(t, func() {
		New(logger).Debug().
			Str("conn", "https://localhost:9200").
			Int("attempts", 3).
			Stringer("nil_conn", (*url.URL)(nil)).
			Err(errors.New("boom")).
			Msg("Node overloaded")
	})
	require.Empty(t, buf.String())
}

// TestNewHandlerContract covers what must still hold because the adapter hands
// records to Handler().Handle rather than to the logger: caller attribution,
// level filtering, and attributes bound with Logger.With.
//
// A row with no wantContains asserts that nothing was written at all, so a row
// can never silently assert nothing.
func TestNewHandlerContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		handlerOpts     *slog.HandlerOptions
		withAttrs       []any
		msg             string
		build           func(debuglog.Event) debuglog.Event
		wantContains    []string
		wantNotContains []string
	}{
		{
			// The adapter builds the record itself and computes the caller frame
			// with runtime.Callers in Msg, because Msg is the call that sits at the
			// emitting site. Adding a frame between the chain's Msg call and
			// runtime.Callers breaks this silently.
			name:            "source is the caller, not the adapter",
			handlerOpts:     &slog.HandlerOptions{AddSource: true, Level: slog.LevelDebug},
			msg:             "Node overloaded",
			wantContains:    []string{"logslog_internal_test.go"},
			wantNotContains: []string{"logslog.go"},
		},
		{
			// Handler().Handle does no filtering of its own, so the level has to be
			// checked before the record is built.
			name:        "handler level still filters",
			handlerOpts: &slog.HandlerOptions{Level: slog.LevelInfo},
			msg:         "Node overloaded",
		},
		{
			name:        "attributes bound with With survive",
			handlerOpts: &slog.HandlerOptions{Level: slog.LevelDebug},
			withAttrs:   []any{"component", "opensearch"},
			msg:         "Node overloaded",
			build: func(e debuglog.Event) debuglog.Event {
				return e.Str("conn", "https://localhost:9200")
			},
			wantContains: []string{"component=opensearch", "conn=https://localhost:9200"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, tt.handlerOpts))
			if tt.withAttrs != nil {
				logger = logger.With(tt.withAttrs...)
			}

			ev := New(logger).Debug()
			if tt.build != nil {
				ev = tt.build(ev)
			}
			ev.Msg(tt.msg)

			if len(tt.wantContains) == 0 {
				require.Empty(t, buf.String())
				return
			}
			for _, want := range tt.wantContains {
				require.Contains(t, buf.String(), want)
			}
			for _, notWant := range tt.wantNotContains {
				require.NotContains(t, buf.String(), notWant)
			}
		})
	}
}

// TestDefault covers Default()'s two documented properties: slog's package-level
// logger filters debug records out until the application installs a LevelDebug
// handler, and the logger is read per record rather than captured.
//
// Every row constructs the DebugLogger before calling slog.SetDefault, which is
// the order an application produces when it builds a client config and
// configures logging in main. That ordering is what pins per-record resolution:
// an adapter that captured slog.Default() at construction would see the
// pre-test logger and the delivering rows would fail.
//
// Not parallel, and each row restores the previous default: slog.SetDefault is
// process-wide, so these rows would race each other and any other test touching
// the global.
func TestDefault(t *testing.T) {
	tests := []struct {
		name         string
		handlerOpts  *slog.HandlerOptions
		msg          string
		build        func(debuglog.Event) debuglog.Event
		wantContains []string
	}{
		{
			name:        "handler below debug drops records",
			handlerOpts: nil,
			msg:         "Node overloaded",
		},
		{
			name:        "handler admitting debug delivers",
			handlerOpts: &slog.HandlerOptions{Level: slog.LevelDebug},
			msg:         "Node overloaded",
			build: func(e debuglog.Event) debuglog.Event {
				return e.Str("conn", "https://localhost:9200")
			},
			wantContains: []string{`msg="Node overloaded"`, "conn=https://localhost:9200"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previous := slog.Default()
			t.Cleanup(func() { slog.SetDefault(previous) })

			debugLogger := Default()

			var buf bytes.Buffer
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, tt.handlerOpts)))

			ev := debugLogger.Debug()
			if tt.build != nil {
				ev = tt.build(ev)
			}
			ev.Msg(tt.msg)

			if len(tt.wantContains) == 0 {
				require.Empty(t, buf.String())
				return
			}
			for _, want := range tt.wantContains {
				require.Contains(t, buf.String(), want)
			}
		})
	}
}

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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  string
		kv   []any
		want string
	}{
		{
			name: "message only",
			msg:  "Discovery: starting",
			want: `level=DEBUG msg="Discovery: starting"`,
		},
		{
			name: "key value pairs",
			msg:  "Node overloaded",
			kv:   []any{"conn", "https://localhost:9200", "heap_used_percent", 93},
			want: `level=DEBUG msg="Node overloaded" conn=https://localhost:9200 heap_used_percent=93`,
		},
		{
			name: "error value",
			msg:  "Discovery failed",
			kv:   []any{"err", errors.New("connection refused")},
			want: `level=DEBUG msg="Discovery failed" err="connection refused"`,
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

			New(slog.New(handler)).Debug(tt.msg, tt.kv...)

			require.Equal(t, tt.want, strings.TrimSpace(buf.String()))
		})
	}
}

// TestNewHandlerContract covers what must still hold because the adapter hands
// records to Handler().Handle rather than to the logger: caller attribution,
// level filtering, attributes bound with Logger.With, and malformed pairs.
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
		kv              []any
		wantContains    []string
		wantNotContains []string
	}{
		{
			// The adapter builds the record itself and computes the caller frame
			// with runtime.Callers, because calling (*slog.Logger).Debug from inside
			// a wrapper method would attribute every record to the wrapper. Adding
			// a frame inside adapter.Debug breaks this silently.
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
			name:         "attributes bound with With survive",
			handlerOpts:  &slog.HandlerOptions{Level: slog.LevelDebug},
			withAttrs:    []any{"component", "opensearch"},
			msg:          "Node overloaded",
			kv:           []any{"conn", "https://localhost:9200"},
			wantContains: []string{"component=opensearch", "conn=https://localhost:9200"},
		},
		{
			// Parity with both slog and the client's built-in logger. log-zerolog
			// drops the pair instead, which is zerolog's own behavior.
			name:         "dangling key renders as BADKEY",
			handlerOpts:  &slog.HandlerOptions{Level: slog.LevelDebug},
			msg:          "Pool resurrect",
			kv:           []any{"conn", "node-1", "state"},
			wantContains: []string{"!BADKEY=state"},
		},
		{
			name:         "non-string key renders as BADKEY and resyncs",
			handlerOpts:  &slog.HandlerOptions{Level: slog.LevelDebug},
			msg:          "Pool resurrect",
			kv:           []any{42, "conn", "node-1"},
			wantContains: []string{"!BADKEY=42", "conn=node-1"},
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

			New(logger).Debug(tt.msg, tt.kv...)

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
		kv           []any
		wantContains []string
	}{
		{
			name:        "handler below debug drops records",
			handlerOpts: nil,
			msg:         "Node overloaded",
		},
		{
			name:         "handler admitting debug delivers",
			handlerOpts:  &slog.HandlerOptions{Level: slog.LevelDebug},
			msg:          "Node overloaded",
			kv:           []any{"conn", "https://localhost:9200"},
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

			debugLogger.Debug(tt.msg, tt.kv...)

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

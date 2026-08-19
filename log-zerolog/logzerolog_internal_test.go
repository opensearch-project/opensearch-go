// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package logzerolog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Parallel()

	connURL, err := url.Parse("https://localhost:9200/path?q=1")
	require.NoError(t, err)

	tests := []struct {
		name string
		msg  string
		kv   []any
		want string
	}{
		{
			name: "message only",
			msg:  "Discovery: starting",
			want: `{"level":"debug","message":"Discovery: starting"}`,
		},
		{
			name: "key value pairs",
			msg:  "Node overloaded",
			kv:   []any{"conn", "https://localhost:9200", "heap_used_percent", 93},
			want: `{"level":"debug","conn":"https://localhost:9200","heap_used_percent":93,"message":"Node overloaded"}`,
		},
		{
			// A *url.URL is the most common value the client logs. zerolog has no
			// fmt.Stringer case, so without the pre-pass this renders as a
			// ten-field object instead of the URL.
			name: "url renders as a string",
			msg:  "Seed fallback",
			kv:   []any{"conn", connURL},
			want: `{"level":"debug","conn":"https://localhost:9200/path?q=1","message":"Seed fallback"}`,
		},
		{
			// time.Duration implements fmt.Stringer, so a Stringer-first pre-pass
			// would render "1.5s" and defeat zerolog's DurationFieldUnit.
			name: "duration keeps zerolog rendering",
			msg:  "Resurrect scheduled",
			kv:   []any{"timeout", 1500 * time.Millisecond},
			want: `{"level":"debug","timeout":1500,"message":"Resurrect scheduled"}`,
		},
		{
			// A nil *url.URL panics inside (*url.URL).String. The printf
			// formatting this adapter replaced was panic-safe, because fmt
			// recovers a String panic and prints <nil>, and so is zerolog's own
			// reflection fallback. The pre-pass must not regress that.
			name: "nil url does not panic",
			msg:  "Seed fallback",
			kv:   []any{"conn", (*url.URL)(nil)},
			want: `{"level":"debug","conn":null,"message":"Seed fallback"}`,
		},
		{
			// time.Time also implements fmt.Stringer, and its String method
			// renders "2026-08-19 04:13:43.91 +0000 UTC" rather than zerolog's
			// configured TimeFieldFormat.
			name: "time keeps zerolog rendering",
			msg:  "Connection dead",
			kv:   []any{"dead_since", time.Date(2026, 8, 19, 4, 13, 43, 0, time.UTC)},
			want: `{"level":"debug","dead_since":"2026-08-19T04:13:43Z","message":"Connection dead"}`,
		},
		{
			// net.IP implements fmt.Stringer, so the pre-pass has to skip it for
			// zerolog's AppendIPAddr path to render it.
			name: "ip keeps zerolog rendering",
			msg:  "Resolved node",
			kv:   []any{"addr", net.ParseIP("10.0.0.7")},
			want: `{"level":"debug","addr":"10.0.0.7","message":"Resolved node"}`,
		},
		{
			// json.RawMessage has no String method, so it never enters the
			// pre-pass; this pins that it still reaches zerolog as raw JSON
			// rather than a base64 []byte.
			name: "raw json is embedded",
			msg:  "Node stats",
			kv:   []any{"stats", json.RawMessage(`{"heap":93}`)},
			want: `{"level":"debug","stats":{"heap":93},"message":"Node stats"}`,
		},
		{
			name: "error goes through ErrorMarshalFunc",
			msg:  "Discovery failed",
			kv:   []any{"err", errors.New("connection refused")},
			want: `{"level":"debug","err":"connection refused","message":"Discovery failed"}`,
		},
		{
			// The client's connection-state keys carry a named integer type with a
			// String method, so the pre-pass is not URL-specific: without it
			// zerolog's reflection writes the number and the state names are lost.
			name: "named integer Stringer renders as a string",
			msg:  "casLifecycle failed",
			kv:   []any{"state", lifecycleStringer(6)},
			want: `{"level":"debug","state":"lc(6)","message":"casLifecycle failed"}`,
		},
		{
			// Documented divergence from the client's built-in logger, which
			// renders a dangling key as !BADKEY: zerolog drops it silently.
			name: "dangling key is dropped",
			msg:  "Pool resurrect",
			kv:   []any{"conn", "node-1", "state"},
			want: `{"level":"debug","conn":"node-1","message":"Pool resurrect"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			New(zerolog.New(&buf)).Debug(tt.msg, tt.kv...)

			require.JSONEq(t, tt.want, buf.String())
		})
	}
}

// panickingStringer models a value whose String method fails for its own
// reasons, which the pre-pass has to survive because the paths it replaced do.
type panickingStringer struct{}

func (panickingStringer) String() string { panic("String is broken") }

// lifecycleStringer models opensearchtransport's connLifecycle: a named integer
// type whose String method names the bits it holds. It is the second kind of
// Stringer the client emits, after *url.URL, which is why the pre-pass is
// type-agnostic rather than a URL special case.
type lifecycleStringer int

func (l lifecycleStringer) String() string { return fmt.Sprintf("lc(%d)", int(l)) }

// nilMapStringer has a nil-map receiver kind, which the pointer check alone
// would miss.
type nilMapStringer map[string]string

func (m nilMapStringer) String() string { return m["missing"] }

// TestHostileStringers pins that no value can take the process down through the
// pre-pass. fmt recovers a panicking String and prints <nil>-style output, and
// zerolog's reflection fallback renders whatever it can, so this adapter must
// not be the one link in the chain that crashes.
func TestHostileStringers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
	}{
		{name: "nil pointer", value: (*url.URL)(nil)},
		{name: "nil map with String", value: nilMapStringer(nil)},
		{name: "nil slice with String", value: net.IP(nil)},
		{name: "panicking String", value: panickingStringer{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			require.NotPanics(t, func() {
				New(zerolog.New(&buf)).Debug("hostile value", "conn", tt.value)
			})
			require.NotEmpty(t, buf.String())
		})
	}
}

// TestNewDoesNotMutateCallerSlice pins that the Stringer pre-pass copies rather
// than writing back into the slice it was handed.
func TestNewDoesNotMutateCallerSlice(t *testing.T) {
	t.Parallel()

	connURL, err := url.Parse("https://localhost:9200")
	require.NoError(t, err)

	kv := []any{"conn", connURL}
	New(zerolog.New(&bytes.Buffer{})).Debug("Seed fallback", kv...)

	require.Equal(t, []any{"conn", connURL}, kv)
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

	debugLogger.Debug("Node overloaded", "conn", "https://localhost:9200")

	require.JSONEq(
		t,
		`{"level":"debug","conn":"https://localhost:9200","message":"Node overloaded"}`,
		buf.String(),
	)
}

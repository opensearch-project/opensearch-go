// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package logzerolog adapts [zerolog] to the OpenSearch client's debug logger.
//
// The client emits its internal debug records (connection lifecycle
// transitions, discovery results, routing decisions) through
// [opensearchtransport.DebugLogger]. Install one of these adapters to route
// those records into an application's existing zerolog logger:
//
//	client, err := opensearch.NewClient(opensearch.Config{
//		DebugLogger: logzerolog.Default(),
//	})
//
// Records are emitted at zerolog's debug level, which its package-level logger
// admits without further configuration.
package logzerolog

import (
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// loggerFunc resolves the logger a record is written to. New captures the logger
// it was handed; Default reads zerolog's package-level logger per record, so an
// application that reconfigures log.Logger after building its client config is
// still honored. Reading it per record is also what zerolog's own log.Debug()
// does.
type loggerFunc func() zerolog.Logger

type adapter struct{ logger loggerFunc }

// Debug emits msg with the key/value pairs as zerolog fields.
//
// A trailing key with no value is dropped, as is a pair whose key is not a
// string: that is zerolog's own handling of a malformed field slice, and the
// client's built-in logger renders !BADKEY for the same input.
func (a adapter) Debug(msg string, kv ...any) {
	// (zerolog.Logger).Debug has a pointer receiver, so the resolved logger needs
	// a name to be addressable.
	zl := a.logger()
	zl.Debug().Fields(flattenStringers(kv)).Msg(msg)
}

// New returns a DebugLogger writing to zl.
func New(zl zerolog.Logger) opensearchtransport.DebugLogger {
	return adapter{func() zerolog.Logger { return zl }}
}

// Default returns a DebugLogger writing to zerolog's package-level logger, so
// the client inherits whatever format, level, and writer the application has
// already configured. The logger is read per record, so a reassignment of
// log.Logger that runs after this call still applies.
func Default() opensearchtransport.DebugLogger {
	return adapter{func() zerolog.Logger { return log.Logger }}
}

// flattenStringers replaces each value implementing [fmt.Stringer] with its
// String result, returning kv unchanged when there is nothing to replace.
//
// zerolog's field encoder has no fmt.Stringer case, so a value it does not
// recognize falls through to reflection-based JSON. The client's most common
// debug field is a *url.URL, which would render as a ten-field object rather
// than the address.
//
// Values zerolog renders itself are left alone so their configured formatting
// survives -- see [isZerologNative].
func flattenStringers(kv []any) []any {
	var out []any
	for i, v := range kv {
		s, ok := v.(fmt.Stringer)
		if !ok || isZerologNative(v) || isNilPointer(v) {
			continue
		}
		if out == nil {
			out = slices.Clone(kv)
		}
		out[i] = safeString(s)
	}
	if out == nil {
		return kv
	}
	return out
}

// safeString calls s.String, turning a panic into a placeholder rather than
// letting it escape.
//
// fmt does the same for %v, rendering %!v(PANIC=String method: ...), so the
// printf formatting this pre-pass replaced tolerated a broken String. A debug
// field is not worth killing the process for, and this adapter must not be the
// least forgiving link in the chain.
func safeString(s fmt.Stringer) string {
	var v string
	func() {
		defer func() {
			if r := recover(); r != nil {
				v = fmt.Sprintf("!PANIC(String method: %v)", r)
			}
		}()
		v = s.String()
	}()
	return v
}

// isNilPointer reports whether v holds a nil pointer, so that String is not
// called on it.
//
// A typed-nil pointer satisfies fmt.Stringer, and many String methods
// dereference without a guard: (*url.URL).String panics. Both paths this
// pre-pass sits in front of tolerate that already, so it must not regress them.
// fmt recovers a panicking String and prints <nil>, and zerolog's reflection
// fallback renders null. Skipping the value here leaves zerolog to render null.
func isNilPointer(v any) bool {
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

// isZerologNative reports whether zerolog encodes v in a way the pre-pass would
// discard. Each of these implements fmt.Stringer and would otherwise be caught:
//
//   - time.Duration is written according to DurationFieldUnit and
//     DurationFieldInteger, not as "1.5s".
//   - time.Time is written according to TimeFieldFormat, not as
//     "2026-08-19 04:13:43.91 +0000 UTC".
//   - error is written through ErrorMarshalFunc, and its String result would
//     bypass any configured stack or structured rendering.
//
// Two types zerolog also encodes itself need no entry. net.IP is a Stringer, but
// zerolog's AppendIPAddr writes exactly ip.String(), so flattening it is
// indistinguishable from letting zerolog handle it. json.RawMessage has no
// String method, so it never reaches the assertion and stays raw JSON. Both are
// covered by tests, so a future zerolog change that makes either case matter
// fails here rather than silently altering output.
func isZerologNative(v any) bool {
	switch v.(type) {
	case time.Duration, *time.Duration, time.Time, *time.Time, error:
		return true
	default:
		return false
	}
}

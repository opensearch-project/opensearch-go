// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package logzerolog adapts [zerolog] to the OpenSearch client's debug logger.
//
// The client emits its internal debug records (connection lifecycle
// transitions, discovery results, routing decisions) through
// [debuglog.Logger]. Install one of these adapters to route those records into
// an application's existing zerolog logger:
//
//	client, err := opensearch.NewClient(opensearch.Config{
//		DebugLogger: logzerolog.Default(),
//	})
//
// Records are emitted at zerolog's debug level, which its package-level logger
// admits without further configuration.
//
// [debuglog.Event] is shaped after zerolog's own builder, so every field method
// forwards to the matching *zerolog.Event method and the value is never boxed
// into an interface on the way. Whatever the application configured for durations
// (DurationFieldUnit), timestamps (TimeFieldFormat), and errors
// (ErrorMarshalFunc) therefore still applies.
package logzerolog

import (
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/opensearch-project/opensearch-go/v5/debuglog"
)

// loggerFunc resolves the logger a record is written to. New captures the logger
// it was handed; Default reads zerolog's package-level logger per record, so an
// application that reconfigures log.Logger after building its client config is
// still honored. Reading it per record is also what zerolog's own log.Debug()
// does.
type loggerFunc func() zerolog.Logger

type adapter struct{ logger loggerFunc }

// Debug begins a record, or discards it when the logger's level excludes debug.
//
// zerolog returns a nil *Event for a filtered-out level. Its own methods tolerate
// that, but returning [debuglog.Nop] instead keeps the nil from traveling through
// this package and means no *Event is taken from zerolog's pool for a record that
// will not be written.
func (a adapter) Debug() debuglog.Event {
	// (zerolog.Logger).Debug has a pointer receiver, so the resolved logger needs
	// a name to be addressable.
	zl := a.logger()
	//nolint:zerologlint // dispatched by event.Msg, which zerologlint cannot follow through the debuglog.Event interface
	e := zl.Debug()
	if e == nil {
		return debuglog.Nop()
	}
	return event{e}
}

// event forwards each field to zerolog.
//
// It holds nothing but the *zerolog.Event, which makes it pointer-shaped, so
// converting it to [debuglog.Event] on every chained call needs no allocation.
// Each zerolog method returns the same *Event it was called on, so the result is
// discarded and the receiver returned instead.
//
// A chain that never reaches Msg leaves the *Event unreturned to zerolog's pool.
// The osapilint check in this repository reports such a chain.
type event struct{ e *zerolog.Event }

// Str implements [debuglog.Event].
func (ev event) Str(key, val string) debuglog.Event {
	ev.e.Str(key, val)
	return ev
}

// Strs implements [debuglog.Event].
func (ev event) Strs(key string, val []string) debuglog.Event {
	ev.e.Strs(key, val)
	return ev
}

// Int implements [debuglog.Event].
func (ev event) Int(key string, val int) debuglog.Event {
	ev.e.Int(key, val)
	return ev
}

// Int32 implements [debuglog.Event].
func (ev event) Int32(key string, val int32) debuglog.Event {
	ev.e.Int32(key, val)
	return ev
}

// Int64 implements [debuglog.Event].
func (ev event) Int64(key string, val int64) debuglog.Event {
	ev.e.Int64(key, val)
	return ev
}

// Uint32 implements [debuglog.Event].
func (ev event) Uint32(key string, val uint32) debuglog.Event {
	ev.e.Uint32(key, val)
	return ev
}

// Float64 implements [debuglog.Event].
func (ev event) Float64(key string, val float64) debuglog.Event {
	ev.e.Float64(key, val)
	return ev
}

// Dur implements [debuglog.Event], so DurationFieldUnit still applies.
func (ev event) Dur(key string, val time.Duration) debuglog.Event {
	ev.e.Dur(key, val)
	return ev
}

// Time implements [debuglog.Event], so TimeFieldFormat still applies.
func (ev event) Time(key string, val time.Time) debuglog.Event {
	ev.e.Time(key, val)
	return ev
}

// Stringer implements [debuglog.Event], resolving the value through
// [debuglog.StringerText] rather than zerolog's own Stringer, which dereferences
// without a nil check. The client's most common debug value is a *url.URL, and a
// nil one would panic.
func (ev event) Stringer(key string, val fmt.Stringer) debuglog.Event {
	ev.e.Str(key, debuglog.StringerText(val))
	return ev
}

// Err implements [debuglog.Event], recording the error under zerolog's configured
// ErrorFieldName, which is "error" by default rather than the "err" the built-in
// logger uses. Going through zerolog's own Err is what keeps ErrorMarshalFunc and
// ErrorStackMarshaler working.
func (ev event) Err(err error) debuglog.Event {
	ev.e.Err(err)
	return ev
}

// Msg implements [debuglog.Event], emitting the record and returning its *Event
// to zerolog's pool.
func (ev event) Msg(msg string) { ev.e.Msg(msg) }

// New returns a debug logger writing to zl.
func New(zl zerolog.Logger) debuglog.Logger {
	return adapter{func() zerolog.Logger { return zl }}
}

// Default returns a debug logger writing to zerolog's package-level logger, so
// the client inherits whatever format, level, and writer the application has
// already configured. The logger is read per record, so a reassignment of
// log.Logger that runs after this call still applies.
func Default() debuglog.Logger {
	return adapter{func() zerolog.Logger { return log.Logger }}
}

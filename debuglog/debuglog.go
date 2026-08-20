// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Package debuglog defines the interface the client emits its internal debug
// records through, and nothing else. It imports only the standard library, so an
// adapter for a logging library depends on this package alone rather than on
// opensearchtransport and everything that package pulls in.
//
// The client's own records are emitted from opensearchtransport, which installs
// the logger and exposes an accessor that never returns nil. Install a logger
// with opensearch.Config.DebugLogger, or set OPENSEARCH_GO_DEBUG or
// Config.EnableDebugLogger to use the built-in one that writes plain text to
// stderr.
//
// The log-zerolog and log-slog submodules implement Logger for those two
// libraries. Any other logger can implement it directly: no method signature
// here mentions a client type, so an implementation needs no import beyond this
// package.
package debuglog

import (
	"fmt"
	"reflect"
	"time"
)

// Logger receives the client's internal debug records: connection lifecycle
// transitions, discovery results, routing decisions, and the like.
//
// Implementations must be safe for concurrent use. Records are emitted from the
// transport's background goroutines as well as from request paths, so Debug may
// be called from several goroutines at once. The returned Event need not be,
// because it belongs to the one caller that is building the record.
//
// Return [Nop] from Debug to discard a record cheaply, for instance when the
// underlying library's own level filter is above debug.
type Logger interface {
	Debug() Event
}

// Event accumulates the fields of one debug record. Each method returns the
// Event so calls chain, and [Event.Msg] emits the record and ends the chain:
//
//	opensearchtransport.Debug().
//		Stringer("conn", conn.URL).
//		Int("attempts", n).
//		Msg("Retrying request")
//
// A chain that never reaches Msg emits nothing, and the compiler cannot say so:
// a chain is a valid expression statement whether or not it terminates. This
// repository guards against it with a test in the debuglog package that parses
// every module and fails when it finds one, so a missing terminator breaks the
// test suite rather than shipping. That guard matches a chain written as a
// statement, which is how every emitting site is written; a chain assigned to a
// variable first is outside what it can see.
//
// The key/value pairing is part of the method signature rather than a
// convention, so a record cannot carry a key with no value or a value with no
// key. Methods exist only for the types the client actually logs; there is no
// escape hatch that takes any, because one would reintroduce the boxing this
// interface exists to avoid.
//
// An implementation must not panic on a nil value. [Event.Stringer] is the case
// that matters, since it defers String to emit time and the client's most common
// debug value is a *url.URL: use [StringerText] rather than calling String
// directly.
//
// Implement all of it. Embedding an Event to inherit most of the methods fails
// quietly: a promoted field method returns the embedded value rather than the
// embedding type, so the chain leaves the implementation at the first field and
// its Msg never runs.
//
// The method count is the deliberate cost of the design. One method per logged
// type is what keeps a value out of an interface on its way to the logger, and
// the census of the client's own records needs every one of these. An Any(key
// string, val any) escape hatch would shrink the interface and reintroduce
// exactly the boxing it exists to avoid.
type Event interface { //nolint:interfacebloat // one method per logged type is the point; see above
	// Str adds a string field.
	Str(key, val string) Event
	// Strs adds a string slice field.
	Strs(key string, val []string) Event
	// Int adds an int field.
	Int(key string, val int) Event
	// Int32 adds an int32 field.
	Int32(key string, val int32) Event
	// Int64 adds an int64 field.
	Int64(key string, val int64) Event
	// Uint32 adds a uint32 field.
	Uint32(key string, val uint32) Event
	// Float64 adds a float64 field.
	Float64(key string, val float64) Event
	// Dur adds a duration field. The rendering is the implementation's, so a
	// library configured to log durations as milliseconds still does.
	Dur(key string, val time.Duration) Event
	// Time adds a timestamp field, rendered in the implementation's format.
	Time(key string, val time.Time) Event
	// Stringer adds a field holding val's String result. String is called only
	// if the record is emitted, so passing a value whose String is expensive
	// costs nothing when debug logging is off.
	Stringer(key string, val fmt.Stringer) Event
	// Err adds the record's error. The key is the implementation's: the built-in
	// logger uses "err", while an adapter uses whatever its library configures,
	// so that a library's own error marshaling still applies. A record carries
	// at most one error; a second one belongs in its own record.
	Err(err error) Event
	// Msg emits the record with msg as its message and ends the chain.
	Msg(msg string)
}

// StringerText returns val's String result, or "<nil>" when val is nil or holds
// a nil pointer.
//
// Implementations of [Event.Stringer] should use this instead of calling String
// directly. A nil *url.URL satisfies fmt.Stringer, so the interface value is
// non-nil while the pointer inside it is not, and (*url.URL).String dereferences
// its receiver. Debug logging must not be able to panic the program it is
// describing, and *url.URL is the client's most common debug value.
//
// Only a nil pointer is guarded. A String method on any other nil-able kind
// either works or is the caller's bug to fix. The reflection happens once per
// emitted field and never on the path a disabled logger takes.
func StringerText(val fmt.Stringer) string {
	if val == nil {
		return NilText
	}
	if rv := reflect.ValueOf(val); rv.Kind() == reflect.Pointer && rv.IsNil() {
		return NilText
	}
	return val.String()
}

// NilText renders a nil value, matching the way the fmt package prints one.
//
// It is exported because an implementation needs it to honor the same contract
// [StringerText] does for a nil Stringer: [Event.Err] handed a nil error, and any
// other absent value, should read the same way whichever logger is installed.
const NilText = "<nil>"

// Nop returns an Event that discards the record. Its methods do nothing and it
// allocates nothing, so a disabled logger costs only the chained calls
// themselves.
//
// Note that the arguments to those calls are still evaluated: Go has no way to
// abandon a method chain partway. A record whose fields are expensive to compute
// should pass them lazily via [Event.Stringer].
func Nop() Event { return nopEvent{} }

// nopEvent discards every field. It is empty so that converting it to Event
// needs no allocation.
type nopEvent struct{}

// Str implements [Event].
func (e nopEvent) Str(_, _ string) Event { return e }

// Strs implements [Event].
func (e nopEvent) Strs(_ string, _ []string) Event { return e }

// Int implements [Event].
func (e nopEvent) Int(_ string, _ int) Event { return e }

// Int32 implements [Event].
func (e nopEvent) Int32(_ string, _ int32) Event { return e }

// Int64 implements [Event].
func (e nopEvent) Int64(_ string, _ int64) Event { return e }

// Uint32 implements [Event].
func (e nopEvent) Uint32(_ string, _ uint32) Event { return e }

// Float64 implements [Event].
func (e nopEvent) Float64(_ string, _ float64) Event { return e }

// Dur implements [Event].
func (e nopEvent) Dur(_ string, _ time.Duration) Event { return e }

// Time implements [Event].
func (e nopEvent) Time(_ string, _ time.Time) Event { return e }

// Stringer implements [Event]. The value's String method is never called, which
// is what makes a deferred field free when logging is off.
func (e nopEvent) Stringer(_ string, _ fmt.Stringer) Event { return e }

// Err implements [Event].
func (e nopEvent) Err(_ error) Event { return e }

// Msg implements [Event], discarding the record.
func (e nopEvent) Msg(_ string) {}

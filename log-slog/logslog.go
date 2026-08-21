// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package logslog adapts [log/slog] to the OpenSearch client's debug logger.
//
// The client emits its internal debug records (connection lifecycle
// transitions, discovery results, routing decisions) through
// [debuglog.Logger]. Install one of these adapters to route those records into
// an application's existing slog logger:
//
//	client, err := opensearch.NewClient(opensearch.Config{
//		DebugLogger: logslog.New(logger),
//	})
//
// Debug records are emitted at [slog.LevelDebug], so the handler must be
// configured to admit that level. See [Default] for the common surprise.
//
// slog is in the standard library, so unlike the log-zerolog module this one
// keeps no dependency out of the client's graph. It exists because
// [debuglog.Event] is a builder and slog's API is not, so something has to map
// between them, and because it presents the same New/Default surface that a
// consumer writing an adapter for a third logging library can copy.
package logslog

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/opensearch-project/opensearch-go/v5/debuglog"
)

// loggerFunc resolves the logger a record is written to. New captures the logger
// it was handed; Default resolves slog's package-level logger per record, so an
// application that calls slog.SetDefault after building its client config is
// still honored.
type loggerFunc func() *slog.Logger

type adapter struct{ logger loggerFunc }

// eventPool holds events between records so that a logger left switched on does
// not allocate one, plus its attribute slice, per record.
//
// An event is returned by [event.Msg]. A chain that never reaches Msg leaves its
// event to the garbage collector, which is why the pool is a performance measure
// and not a correctness one.
//
//nolint:gochecknoglobals // a pool is process-wide by nature; the transport's own event pool is excluded for the same reason
var eventPool = sync.Pool{New: func() any { return new(event) }}

// maxPooledAttrs bounds the attribute capacity the pool retains, so one unusually
// wide record cannot park its slice for the life of the process. The client's
// widest record carries six fields, in pool_congestion.go's AIMD adjustment.
const maxPooledAttrs = 32

// Debug begins a record, or discards it when the handler does not admit
// [slog.LevelDebug].
//
// The level is checked here rather than in Msg so that a filtered-out record
// accumulates no attributes at all: the returned [debuglog.Nop] discards every
// field it is handed, and no event is taken from the pool. Handler().Handle in Msg
// does no filtering of its own, so without this check a LevelInfo handler would
// receive debug records.
func (a adapter) Debug() debuglog.Event {
	l := a.logger()
	if !l.Enabled(context.Background(), slog.LevelDebug) {
		return debuglog.Nop()
	}
	e, _ := eventPool.Get().(*event)
	e.logger = l
	e.attrs = e.attrs[:0]
	return e
}

// event accumulates one record's attributes for slog.
//
// The record is assembled and handed to the handler directly rather than passed
// to (*slog.Logger).Debug so that HandlerOptions.AddSource reports the client
// code that emitted it. Delegating to Debug would attribute every record to this
// file.
//
// Both the event and its attribute slice survive in eventPool across records, so
// a steady stream of records reuses one slice rather than growing a new one from
// empty each time.
type event struct {
	logger *slog.Logger
	attrs  []slog.Attr
}

func (e *event) add(attr slog.Attr) debuglog.Event {
	e.attrs = append(e.attrs, attr)
	return e
}

// Str implements [debuglog.Event].
func (e *event) Str(key, val string) debuglog.Event { return e.add(slog.String(key, val)) }

// Strs implements [debuglog.Event].
func (e *event) Strs(key string, val []string) debuglog.Event { return e.add(slog.Any(key, val)) }

// Int implements [debuglog.Event].
func (e *event) Int(key string, val int) debuglog.Event { return e.add(slog.Int(key, val)) }

// Int32 implements [debuglog.Event]. It widens, because slog offers no attribute
// constructor narrower than 64 bits. The rendered value is the same either way.
func (e *event) Int32(key string, val int32) debuglog.Event {
	return e.add(slog.Int64(key, int64(val)))
}

// Int64 implements [debuglog.Event].
func (e *event) Int64(key string, val int64) debuglog.Event { return e.add(slog.Int64(key, val)) }

// Uint32 implements [debuglog.Event]. It widens, for the same reason as Int32.
func (e *event) Uint32(key string, val uint32) debuglog.Event {
	return e.add(slog.Uint64(key, uint64(val)))
}

// Float64 implements [debuglog.Event].
func (e *event) Float64(key string, val float64) debuglog.Event {
	return e.add(slog.Float64(key, val))
}

// Dur implements [debuglog.Event].
func (e *event) Dur(key string, val time.Duration) debuglog.Event {
	return e.add(slog.Duration(key, val))
}

// Time implements [debuglog.Event].
func (e *event) Time(key string, val time.Time) debuglog.Event { return e.add(slog.Time(key, val)) }

// Stringer implements [debuglog.Event], resolving the value now rather than
// handing slog the fmt.Stringer, because slog would render it through the fmt
// package and panic on a nil pointer. Resolving here is safe: Debug already
// returned a Nop if the record was going to be dropped, so reaching this method
// means the record is emitted.
func (e *event) Stringer(key string, val fmt.Stringer) debuglog.Event {
	return e.add(slog.String(key, debuglog.StringerText(val)))
}

// Err implements [debuglog.Event], recording the error under the key "err".
func (e *event) Err(err error) debuglog.Event { return e.add(slog.Any(errKey, err)) }

// Msg emits the record.
//
// The caller's program counter is taken here rather than in Debug because Msg is
// the call that sits at the emitting site: whatever frames route Debug to this
// adapter, Msg is invoked directly by the code being described. For a chain
// broken across several lines, the attributed line is the one holding Msg.
func (e *event) Msg(msg string) {
	// skipCallers drops the runtime.Callers and Msg frames so the program counter
	// belongs to whoever ended the chain. Adding a frame inside this method
	// silently misattributes every record; TestNewPreservesSourceAttribution is
	// the guard.
	const skipCallers = 2

	var pcs [1]uintptr
	runtime.Callers(skipCallers, pcs[:])

	record := slog.NewRecord(time.Now(), slog.LevelDebug, msg, pcs[0])
	record.AddAttrs(e.attrs...)

	// Msg returns no error: a debug logger that cannot write has nowhere left to
	// report it.
	_ = e.logger.Handler().Handle(context.Background(), record) //nolint:errcheck // no error path on debuglog.Event.Msg

	// Returning the event here is safe even for a handler that retains the record
	// past Handle: AddAttrs copies into the Record's own storage, so the record
	// never aliases e.attrs.
	e.logger = nil
	if cap(e.attrs) <= maxPooledAttrs {
		eventPool.Put(e)
	}
}

// errKey is the key debuglog.Event.Err records an error under.
const errKey = "err"

// New returns a debug logger writing to l.
func New(l *slog.Logger) debuglog.Logger {
	return adapter{func() *slog.Logger { return l }}
}

// Default returns a debug logger writing to slog's package-level logger, so the
// client inherits whatever handler the application has installed. The logger is
// read per record, so a slog.SetDefault that runs after this call still applies.
//
// Records are dropped unless that handler admits [slog.LevelDebug], which the
// default one does not:
//
//	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
//		Level: slog.LevelDebug,
//	})))
//
// This is where slog and zerolog differ. zerolog's package-level logger emits
// debug records without any configuration, so log-zerolog's Default needs no
// such preparation.
func Default() debuglog.Logger { return adapter{slog.Default} }

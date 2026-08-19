// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package logslog adapts [log/slog] to the OpenSearch client's debug logger.
//
// The client emits its internal debug records (connection lifecycle
// transitions, discovery results, routing decisions) through
// [opensearchtransport.DebugLogger]. Install one of these adapters to route
// those records into an application's existing slog logger:
//
//	client, err := opensearch.NewClient(opensearch.Config{
//		DebugLogger: logslog.New(logger),
//	})
//
// Debug records are emitted at [slog.LevelDebug], so the handler must be
// configured to admit that level. See [Default] for the common surprise.
//
// slog is in the standard library, so unlike the log-zerolog module this one
// keeps no dependency out of the client's graph. It exists so the adapter is
// version-tagged, tested, and reachable at the same import-path shape as
// log-zerolog, and so it presents the same New/Default surface that a consumer
// writing an adapter for a third logging library can copy.
package logslog

import (
	"context"
	"log/slog"
	"runtime"
	"time"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// *slog.Logger's own Debug method already matches DebugLogger. The adapter below
// does not rely on that, but the match is worth asserting: it is what lets a
// caller skip this module entirely and pass a *slog.Logger straight to
// Config.DebugLogger, and nothing else in either package would catch the two
// signatures drifting apart.
var _ opensearchtransport.DebugLogger = (*slog.Logger)(nil)

// loggerFunc resolves the logger a record is written to. New captures the logger
// it was handed; Default resolves slog's package-level logger per record, so an
// application that calls slog.SetDefault after building its client config is
// still honored.
type loggerFunc func() *slog.Logger

type adapter struct{ logger loggerFunc }

// Debug emits msg and the key/value pairs as a debug record.
//
// The record is built here rather than delegated to (*slog.Logger).Debug so that
// HandlerOptions.AddSource reports the client code that emitted the record. A
// wrapper that called Debug would attribute every record to this file.
func (a adapter) Debug(msg string, kv ...any) {
	ctx := context.Background()
	l := a.logger()

	// Handler().Handle below does no level filtering of its own, so records have
	// to be dropped here or a LevelInfo handler would receive them.
	if !l.Enabled(ctx, slog.LevelDebug) {
		return
	}

	// skipCallers drops the runtime.Callers and Debug frames so the program
	// counter belongs to whoever called Debug. Adding a frame inside this method
	// silently misattributes every record; TestNewPreservesSourceAttribution is
	// the guard.
	const skipCallers = 2

	var pcs [1]uintptr
	runtime.Callers(skipCallers, pcs[:])

	record := slog.NewRecord(time.Now(), slog.LevelDebug, msg, pcs[0])
	record.Add(kv...)

	// DebugLogger returns no error: a debug logger that cannot write has nowhere
	// left to report it.
	_ = l.Handler().Handle(ctx, record) //nolint:errcheck // no error path on DebugLogger.Debug
}

// New returns a DebugLogger writing to l.
func New(l *slog.Logger) opensearchtransport.DebugLogger {
	return adapter{func() *slog.Logger { return l }}
}

// Default returns a DebugLogger writing to slog's package-level logger, so the
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
func Default() opensearchtransport.DebugLogger { return adapter{slog.Default} }

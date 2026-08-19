# log-slog

`log-slog` routes the OpenSearch Go client's internal debug records into [`log/slog`](https://pkg.go.dev/log/slog).

The client emits debug records (connection lifecycle transitions, discovery results, routing decisions, pool selection) through the one-method `opensearchtransport.DebugLogger` interface. Those records are off by default. Installing an adapter turns them on and sends them wherever the application already sends its logs, instead of the plain-text stream the client writes on its own.

`log-slog` is a separate Go module. slog is in the standard library, so this costs no dependencies either way; the module exists so the adapter is versioned, tested, and reachable at the same import-path shape as [`log-zerolog`](../log-zerolog), and so both present the same `New`/`Default` surface that a consumer writing an adapter for a third logging library can copy.

## Install

```sh
go get github.com/opensearch-project/opensearch-go/v5/log-slog
```

## Concepts

- **`opensearchtransport.DebugLogger`** is the one-method interface the client emits through: `Debug(msg string, kv ...any)`. Any type with that method satisfies it, including `*slog.Logger` itself.
- **`New`** takes a specific `*slog.Logger`. **`Default`** reads slog's package-level logger, so the client inherits whatever handler the application has installed. Neither installs anything on its own: the returned value goes into `Config.DebugLogger`, and until it does, no debug records are emitted at all.

## Usage

`Default` reads slog's package-level logger:

```go
client, err := opensearch.NewClient(opensearch.Config{
	Addresses:   []string{"https://localhost:9200"},
	DebugLogger: logslog.Default(),
})
```

Pass a specific logger with `New`:

```go
logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
	Level: slog.LevelDebug,
}))

client, err := opensearch.NewClient(opensearch.Config{
	DebugLogger: logslog.New(logger),
})
```

## The level matters

Records are emitted at `slog.LevelDebug`, and slog's default handler discards anything below `LevelInfo`. `logslog.Default()` therefore emits nothing until the application installs a handler that admits debug:

```go
slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
	Level: slog.LevelDebug,
})))
```

This is the one place the two adapters differ. zerolog's package-level logger admits debug records with no setup, so `logzerolog.Default()` needs no equivalent step.

## Source attribution

The adapter builds each record itself instead of calling `(*slog.Logger).Debug`, and computes the caller frame with `runtime.Callers`. A wrapper that delegated to `Debug` would consume the frame slog attributes records to, so `HandlerOptions.AddSource` would report this package rather than the client file that emitted the record. Building the record also means the handler's level has to be checked explicitly, because `Handler().Handle` does no filtering of its own.

Both are pinned by tests. The one to be careful with is the frame count: adding a call inside `adapter.Debug` shifts it and misattributes every record silently.

## Passing a `*slog.Logger` directly

`*slog.Logger` has `Debug(msg string, args ...any)`, which is `DebugLogger` exactly, so it can go straight into `Config.DebugLogger` without this module:

```go
client, err := opensearch.NewClient(opensearch.Config{
	DebugLogger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
})
```

Records carry the client's own call site either way: slog's `Debug` computes the caller frame itself, and `New`'s adapter reconstructs it. What you give up is nothing functional; `New` exists so slog users find the same import-path shape and the same `New`/`Default` pair that zerolog users get.

That signature match is structural rather than declared, so neither package's compilation would notice the two drifting apart. This module asserts it:

```go
var _ opensearchtransport.DebugLogger = (*slog.Logger)(nil)
```

If either side changes, the build fails at that line with both signatures named.

## Performance

`DebugLogger` takes `...any`, which is what slog takes too, so nothing is lost in translation. Debug records are off unless a logger is installed, and every emitting site is nil-guarded. This interface is for development-time diagnostics, not the request hot path.

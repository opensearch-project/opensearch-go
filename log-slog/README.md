# log-slog

`log-slog` routes the OpenSearch Go client's internal debug records into [`log/slog`](https://pkg.go.dev/log/slog).

The client emits debug records (connection lifecycle transitions, discovery results, routing decisions, pool selection) through the `debuglog.Logger` interface, defined in the root module's `debuglog` package. Those records are off by default. Installing an adapter turns them on and sends them wherever the application already sends its logs, instead of the plain-text stream the client writes on its own.

`log-slog` is a separate Go module. slog is in the standard library and `debuglog` imports only the standard library too, so this costs no dependencies either way; the module exists so the adapter is versioned, tested, and reachable at the same import-path shape as [`log-zerolog`](../log-zerolog), and so both present the same `New`/`Default` surface that a consumer writing an adapter for a third logging library can copy.

## Install

```sh
go get github.com/opensearch-project/opensearch-go/v5/log-slog
```

## Concepts

- **`debuglog.Logger`** is the interface the client emits through: `Debug() Event`, where `Event` is a chain of typed field methods (`Str`, `Int`, `Dur`, `Stringer`, `Err`, and the rest) that `Msg` emits and ends. `*slog.Logger` does not implement it: the chain has no counterpart on slog's own type, so a value needs this module's adapter to reach `Config.DebugLogger`.
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

Both are pinned by tests. The one to be careful with is the frame count. The caller's program counter is taken in `Msg` rather than in `Debug`, because `Msg` is the call that sits at the emitting site however many frames routed `Debug` to this adapter. Adding a call between the chain's `Msg` and `runtime.Callers` shifts the frame and misattributes every record silently.

## Performance

Each `debuglog.Event` method accumulates a typed `slog.Attr` directly, with no boxing into `any` along the way. Debug records are off unless a logger is installed, and `opensearchtransport.Debug()` returns a no-op `Event` when none is, so an idle logger costs only the chained calls themselves. This interface is for development-time diagnostics, not the request hot path.

Measured on darwin/arm64, Apple M4 Max, go1.26.4, writing to `io.Discard`, medians of 10 runs through `benchstat`. Reproduce with `go test -run=none -bench=. -benchmem -count=10 ./...` here and in [`../log-zerolog`](../log-zerolog), which benchmarks the same record shapes.

| record           | log-slog (JSONHandler)    | log-slog (TextHandler)    | log-zerolog             |
| ---------------- | ------------------------- | ------------------------- | ----------------------- |
| 1 field          | 377.5 ns, 112 B, 3 allocs | 413.1 ns, 112 B, 3 allocs | 82.6 ns, 32 B, 1 alloc  |
| 4 fields         | 558.6 ns, 352 B, 5 allocs | 703.9 ns, 376 B, 6 allocs | 139.5 ns, 32 B, 1 alloc |
| 8 fields         | 835.1 ns, 809 B, 8 allocs | 929.6 ns, 825 B, 8 allocs | 194.6 ns, 32 B, 1 alloc |
| level rejects it | 6.1 ns, 0 B, 0 allocs     | 6.2 ns, 0 B, 0 allocs     | 10.9 ns, 0 B, 0 allocs  |

slog costs roughly four times what zerolog does per record, and it allocates per attribute, so both its byte count and its allocation count grow with the record where zerolog's stay flat. Part of that is the record rebuild this adapter does to keep source attribution: `runtime.Callers` and `slog.NewRecord` are paid on every emitted record. The handler choice matters too, with `TextHandler` costing more than `JSONHandler` at every size.

Prefer this module when the application already routes everything through `log/slog` and having one logging pipeline is worth more than the per-record cost. Prefer [`log-zerolog`](../log-zerolog) when debug logging will be left on somewhere it matters, or where allocation pressure is something you measure. slog is also the only one of the two that adds no dependency, since it is in the standard library. Both sit far below the cost of the request being described.

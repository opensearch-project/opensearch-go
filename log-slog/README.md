# log-slog

`log-slog` routes the OpenSearch Go client's internal debug records into [`log/slog`](https://pkg.go.dev/log/slog).

The client emits debug records (connection lifecycle transitions, discovery results, routing decisions, pool selection) through the `debuglog.Logger` interface, defined in the root module's `debuglog` package. Those records are off by default. Installing an adapter turns them on and sends them wherever the application already sends its logs, instead of the plain-text stream the client writes on its own.

`log-slog` is a separate Go module. slog is in the standard library and `debuglog` imports only the standard library too, so this costs no dependencies either way; the module exists so the adapter is versioned, tested, and reachable at the same import-path shape as [`log-zerolog`](../log-zerolog), and so both present the same `New`/`Default` surface that a consumer writing an adapter for a third logging library can copy.

## Install

```sh
go get github.com/opensearch-project/opensearch-go/log-slog/v5
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

Each `debuglog.Event` method accumulates a typed `slog.Attr` directly, with no boxing into `any` along the way. The event comes from a `sync.Pool` and its attribute slice is reused.

Records wider than five fields cost one allocation more, because `slog.Record` stores five attributes inline and moves the remainder to the heap. That, and each handler's own encoding, is outside this adapter's control: the record is handed to the handler by value to keep source attribution pointing at the transport file.

See [Cost](../debuglog/README.md#cost) for measured numbers. Reproduce with `go test -run=none -bench=. -benchmem -count=10 ./...`.

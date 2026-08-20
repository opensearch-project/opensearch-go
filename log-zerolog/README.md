# log-zerolog

`log-zerolog` routes the OpenSearch Go client's internal debug records into [zerolog](https://github.com/rs/zerolog).

The client emits debug records (connection lifecycle transitions, discovery results, routing decisions, pool selection) through the `debuglog.Logger` interface, defined in the root module's `debuglog` package. Those records are off by default. Installing an adapter turns them on and sends them wherever the application already sends its logs, instead of the plain-text stream the client writes on its own.

`log-zerolog` is a separate Go module, so zerolog stays out of the core client's dependency graph. It compiles against `debuglog` alone rather than against `opensearchtransport`, so an adapter needs no package of the client beyond the interface it implements. You opt in by importing it. The companion [`log-slog`](../log-slog) module does the same for `log/slog`.

## Install

```sh
go get github.com/opensearch-project/opensearch-go/v5/log-zerolog
```

## Concepts

- **`debuglog.Logger`** is the interface the client emits through: `Debug() Event`, where `Event` is a chain of typed field methods (`Str`, `Int`, `Dur`, `Stringer`, `Err`, and the rest) that `Msg` emits and ends. The client depends on nothing beyond `debuglog`, so no logging library reaches the core module.
- **`New`** wraps a specific `zerolog.Logger`. **`Default`** reads zerolog's package-level `log.Logger`, so the client inherits whatever the application has already configured. Neither installs anything on its own: the returned value goes into `Config.DebugLogger`, and until it does, no debug records are emitted at all.

## Usage

`Default` reads zerolog's package-level logger, so the client picks up the format, level, and writer the application has already configured:

```go
client, err := opensearch.NewClient(opensearch.Config{
	Addresses:   []string{"https://localhost:9200"},
	DebugLogger: logzerolog.Default(),
})
```

Pass a specific logger with `New`:

```go
zl := zerolog.New(os.Stderr).With().Str("component", "opensearch").Logger()

client, err := opensearch.NewClient(opensearch.Config{
	DebugLogger: logzerolog.New(zl),
})
```

Records are emitted at zerolog's debug level, which its package-level logger admits with no further setup. When zerolog's own level filter excludes debug, `Debug()` returns `debuglog.Nop()` rather than an `Event` wrapping zerolog's nil, so a filtered record never takes an `*Event` from zerolog's pool.

## Field rendering

Each `debuglog.Event` method forwards straight to the matching `*zerolog.Event` method: `Int` calls `Int`, `Dur` calls `Dur`, and so on, with no boxing and no pass over the fields beforehand. Whatever the application configured for durations (`DurationFieldUnit`), timestamps (`TimeFieldFormat`), and errors (`ErrorMarshalFunc`) therefore still applies.

`Stringer` is the one field this adapter resolves itself instead of handing to zerolog: it calls `debuglog.StringerText` and writes the result with `Str`, because zerolog's own Stringer dereferences without a nil check. The client's most common debug field is a `*url.URL`, and a nil one would panic.

`Err` records the error under zerolog's configured `ErrorFieldName` (`"error"` by default) rather than the `"err"` the built-in logger and `log-slog` use, because going through zerolog's own `Err` is what keeps `ErrorMarshalFunc` and `ErrorStackMarshaler` working.

## Performance

Each `debuglog.Event` method is a typed call straight through to the matching `*zerolog.Event` method, so no value is boxed into an interface on the way. Debug records are off unless a logger is installed, and `opensearchtransport.Debug()` returns a no-op `Event` when none is, so an idle logger costs only the chained calls themselves. This interface is for development-time diagnostics, not the request hot path.

What that buys, and the reason to reach for this module over [`log-slog`](../log-slog): the allocation count per record does not move with the number of fields. Eight fields cost the same as one, and the single allocation that remains is `(*url.URL).String` building the connection address rather than anything the adapter does.

Measured numbers for both adapters, over identical record shapes, live in one place: [Choosing between the adapters](../USER_GUIDE.md#choosing-between-the-adapters). Reproduce with `go test -run=none -bench=. -benchmem -count=10 ./...` here and in `../log-slog`.

# log-zerolog

`log-zerolog` routes the OpenSearch Go client's internal debug records into [zerolog](https://github.com/rs/zerolog).

The client emits debug records (connection lifecycle transitions, discovery results, routing decisions, pool selection) through the `debuglog.Logger` interface, defined in the root module's `debuglog` package. Those records are off by default. Installing an adapter turns them on and sends them wherever the application already sends its logs, instead of the plain-text stream the client writes on its own.

`log-zerolog` is a separate Go module, so zerolog stays out of the core client's dependency graph. It compiles against `debuglog` alone rather than against `opensearchtransport`, so an adapter needs no package of the client beyond the interface it implements. You opt in by importing it. The companion [`log-slog`](../log-slog) module does the same for `log/slog`.

## Install

```sh
go get github.com/opensearch-project/opensearch-go/log-zerolog/v5
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

`Stringer` resolves its argument through `debuglog.StringerText`, a nil-guarded wrapper around `String()`, and writes the result with `Str`.

`Err` records the error under zerolog's configured `ErrorFieldName` (`"error"` by default) rather than the `"err"` the built-in logger and `log-slog` use, because going through zerolog's own `Err` is what keeps `ErrorMarshalFunc` and `ErrorStackMarshaler` working.

## Performance

See [Cost](../debuglog/README.md#cost) for measured numbers. Reproduce with `go test -run=none -bench=. -benchmem -count=10 ./...`.

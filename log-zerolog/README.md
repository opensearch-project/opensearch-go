# log-zerolog

`log-zerolog` routes the OpenSearch Go client's internal debug records into [zerolog](https://github.com/rs/zerolog).

The client emits debug records (connection lifecycle transitions, discovery results, routing decisions, pool selection) through the one-method `opensearchtransport.DebugLogger` interface. Those records are off by default. Installing an adapter turns them on and sends them wherever the application already sends its logs, instead of the plain-text stream the client writes on its own.

`log-zerolog` is a separate Go module, so zerolog stays out of the core client's dependency graph. You opt in by importing it. The companion [`log-slog`](../log-slog) module does the same for `log/slog`.

## Install

```sh
go get github.com/opensearch-project/opensearch-go/v5/log-zerolog
```

## Concepts

- **`opensearchtransport.DebugLogger`** is the one-method interface the client emits through: `Debug(msg string, kv ...any)`. The client depends on nothing else, so no logging library reaches the core module.
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

Records are emitted at zerolog's debug level, which its package-level logger admits with no further setup.

## Field rendering

Keys and values reach zerolog as fields, so `DurationFieldUnit`, `TimeFieldFormat`, and `ErrorMarshalFunc` all apply as configured.

One value needs help: zerolog's encoder has no `fmt.Stringer` case, and the client's most common debug field is a `*url.URL`, which would fall through to reflection-based JSON and render as a ten-field object. The adapter calls `String` on such values first. Types zerolog encodes itself (`time.Duration`, `time.Time`, `error`) are left alone so their configured formatting survives.

A malformed field slice (a trailing key with no value, or a non-string key) is dropped, which is zerolog's own behavior. The client's built-in logger renders `!BADKEY` for the same input, so malformed pairs look different depending on which logger is installed. Every record the client emits internally is well formed.

## Performance

`DebugLogger` takes `...any`, so values are boxed before they reach zerolog and its zero-allocation encoding does not apply. Debug records are off unless a logger is installed, and every emitting site is nil-guarded, so they cost nothing when off. This interface is for development-time diagnostics, not the request hot path.

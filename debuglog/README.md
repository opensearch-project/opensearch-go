# debuglog

`debuglog` defines the interface `opensearch-go` uses for its internal logging. Log records describe connection lifecycle transitions, discovery results, routing decisions, and pool selection. They are off by default. The package imports only the standard library, so an adapter for a logging library depends on this package alone rather than on `opensearchtransport` and everything that pulls in.

## The interface

```go
type Logger interface {
	Debug() Event
}

type Event interface {
	Str(key, val string) Event
	Strs(key string, val []string) Event
	Int(key string, val int) Event
	Int32(key string, val int32) Event
	Int64(key string, val int64) Event
	Uint32(key string, val uint32) Event
	Float64(key string, val float64) Event
	Dur(key string, val time.Duration) Event
	Time(key string, val time.Time) Event
	Stringer(key string, val fmt.Stringer) Event
	Err(err error) Event
	Msg(msg string)
}
```

A record is one chain, ended by `Msg`:

```go
opensearchtransport.Debug().
	Stringer("conn", conn.URL).
	Int("attempts", n).
	Msg("Retrying request")
```

## Installing a logger

Set `DebugLogger` on `opensearch.Config` or `opensearchtransport.Config`. Two adapter modules implement `Logger`:

- [`log-zerolog`](../log-zerolog) for [zerolog](https://github.com/rs/zerolog)
- [`log-slog`](../log-slog) for the standard library's `log/slog`

Or set `OPENSEARCH_GO_LOG=debug` (or `EnableDebugLogger: true`) for the built-in logger (plain text stderr logger with no external library dependencies).

See [USER_GUIDE.md Debugging](../USER_GUIDE.md#debugging) for the usage walkthrough.

## Cost

The debug logger is allocation-free.\*

Measured August 2026 on darwin/arm64, Apple M4 Pro, go1.26.4, writing to `io.Discard` through `benchstat`. Reproduce with `go test -run=none -bench=. -benchmem -count=10 ./...` in `log-zerolog`, `log-slog`, and the root module. Each module's tests assert these allocation counts as upper bounds to ensure behavior regressions are caught. Byte and allocation counts are exact counters, measured every run, whereas times may fluctuate across hardware (and allocations may change across Go releases).

### Built-in Default Text Logger

Installed by `OPENSEARCH_GO_LOG=debug` or `Config.EnableDebugLogger`.

| field                | time     | bytes | allocs |
| -------------------- | -------- | ----- | ------ |
| `no fields`          | 77.4 ns  | 0 B   | 0      |
| `Str`                | 84.0 ns  | 0 B   | 0      |
| `Strs`               | 99.1 ns  | 0 B   | 0      |
| `Int`                | 85.1 ns  | 0 B   | 0      |
| `Int32`              | 84.5 ns  | 0 B   | 0      |
| `Int64`              | 84.1 ns  | 0 B   | 0      |
| `Uint32`             | 83.3 ns  | 0 B   | 0      |
| `Float64`            | 103.2 ns | 0 B   | 0      |
| `Dur`                | 92.6 ns  | 0 B   | 0      |
| `Time`               | 187.2 ns | 0 B   | 0      |
| `Err`                | 83.8 ns  | 0 B   | 0      |
| `Stringer`           | 111.3 ns | 32 B  | 1      |
| `Stringer, resolved` | 87.1 ns  | 0 B   | 0      |
| no logger installed  | 3.7 ns   | 0 B   | 0      |

### log-zerolog

| field                | time    | bytes | allocs |
| -------------------- | ------- | ----- | ------ |
| `no fields`          | 41.9 ns | 0 B   | 0      |
| `Str`                | 53.0 ns | 0 B   | 0      |
| `Strs`               | 59.5 ns | 0 B   | 0      |
| `Int`                | 49.5 ns | 0 B   | 0      |
| `Int32`              | 49.4 ns | 0 B   | 0      |
| `Int64`              | 49.1 ns | 0 B   | 0      |
| `Uint32`             | 48.1 ns | 0 B   | 0      |
| `Float64`            | 62.7 ns | 0 B   | 0      |
| `Dur`                | 68.7 ns | 0 B   | 0      |
| `Time`               | 71.6 ns | 0 B   | 0      |
| `Err`                | 57.7 ns | 0 B   | 0      |
| `Stringer`           | 82.6 ns | 32 B  | 1      |
| `Stringer, resolved` | 54.1 ns | 0 B   | 0      |
| level rejects it     | 10.5 ns | 0 B   | 0      |

### log-slog

| field                | JSONHandler       | TextHandler       |
| -------------------- | ----------------- | ----------------- |
| `no fields`          | 258.1 ns, 0 B, 0  | 248.0 ns, 0 B, 0  |
| `Str`                | 312.4 ns, 0 B, 0  | 302.5 ns, 0 B, 0  |
| `Strs`               | 394.4 ns, 24 B, 1 | 521.3 ns, 96 B, 5 |
| `Int`                | 299.5 ns, 0 B, 0  | 290.0 ns, 0 B, 0  |
| `Int32`              | 299.7 ns, 0 B, 0  | 290.1 ns, 0 B, 0  |
| `Int64`              | 299.9 ns, 0 B, 0  | 289.8 ns, 0 B, 0  |
| `Uint32`             | 299.1 ns, 0 B, 0  | 288.3 ns, 0 B, 0  |
| `Float64`            | 366.9 ns, 8 B, 1  | 307.9 ns, 0 B, 0  |
| `Dur`                | 303.5 ns, 0 B, 0  | 297.5 ns, 0 B, 0  |
| `Time`               | 348.0 ns, 0 B, 0  | 337.8 ns, 0 B, 0  |
| `Err`                | 317.2 ns, 0 B, 0  | 415.4 ns, 24 B, 1 |
| `Stringer`           | 345.4 ns, 32 B, 1 | 333.6 ns, 32 B, 1 |
| `Stringer, resolved` | 316.8 ns, 0 B, 0  | 305.4 ns, 0 B, 0  |
| level rejects it     | 6.3 ns, 0 B, 0    | 6.3 ns, 0 B, 0    |

The nonzero allocation cost are incurred by slog internal handlers.

### Record width

For the built-in and zerolog logger, cost is linear and proportional to the number of fields in a log message increases. See `BenchmarkTextDebugLoggerRecordWidth` and `BenchmarkAdapterRecordWidth` for details.

| fields | built-in         | log-zerolog      | log-slog (JSONHandler) | log-slog (TextHandler) |
| ------ | ---------------- | ---------------- | ---------------------- | ---------------------- |
| 4      | 109.4 ns, 0 B, 0 | 77.8 ns, 0 B, 0  | 418.7 ns, 0 B, 0       | 415.4 ns, 0 B, 0       |
| 5      | 118.0 ns, 0 B, 0 | 90.2 ns, 0 B, 0  | 452.9 ns, 0 B, 0       | 448.0 ns, 0 B, 0       |
| 6      | 126.3 ns, 0 B, 0 | 99.9 ns, 0 B, 0  | 509.8 ns, 48 B, 1      | 501.6 ns, 48 B, 1      |
| 8      | 143.8 ns, 0 B, 0 | 118.8 ns, 0 B, 0 | 599.9 ns, 128 B, 1     | 591.0 ns, 128 B, 1     |

### Recommendations

There is no cost to logging when it is not enabled (i.e. no allocations or cost to CPU beyond one atomic load).

`log-zerolog` is the recommended production logger. The built-in `debuglog` logger is sufficient and requires no additional module, writes its records to stderr, and performs better than `log/slog`. If absolutely required, [`log-slog`](../log-slog) is available for use. See above benchmarks for details.

---

\* `Stringer` costs one allocation due to calling `String()` on the value passed in rather than in the logger. `Stringer` defers that call until the record is emitted, so a disabled logger never makes an allocation until required.

## Custom Loggers

Tips for implementing a custom logger for `opensearch-go`:

1. **Accept `nil` pointers for arguments.** Debug logging must never panic the program it is describing, which is why `Stringer` must resolve its argument through `StringerText`, a nil-guarded wrapper around `String()`. An interface holding a nil pointer is itself non-nil, so a nil `*url.URL` satisfies `fmt.Stringer` while `(*url.URL).String` dereferences its receiver.

2. **`Debug()` must be safe for concurrent use.**

3. **Return `Nop()` from `Debug` to discard a record cheaply.**

4. See [`log-zerolog/logzerolog.go`](../log-zerolog/logzerolog.go) or [`log-slog/logslog.go`](../log-slog/logslog.go)

5. Only flush and emit a log entry during `Msg()`. A log message must only be emitted when an `Event`'s terminal call reaches its `.Msg()`.

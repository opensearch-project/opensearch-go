# debuglog

`debuglog` defines the interface the OpenSearch Go client emits its internal debug records through, and nothing else.

Those records describe connection lifecycle transitions, discovery results, routing decisions, and pool selection. They are off by default. The package imports only the standard library, so an adapter for a logging library depends on this package alone rather than on `opensearchtransport` and everything that pulls in.

`debuglog` is a package of the root module, not a separate one. Nothing to install beyond the client itself.

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

Two properties drive the rest of the design. Each key's type is fixed by the method carrying it, so a record cannot hold a key with no value or a value with no key, and there is no `!BADKEY` case to render. And `Stringer` defers `String()` to emit time, which is what makes an expensive field free when logging is off.

There is deliberately no `Any(key string, val any)`. It would shrink the interface and reintroduce exactly the boxing the typed methods exist to avoid, and it would become the path of least resistance for every future call site.

## Installing a logger

Set `DebugLogger` on `opensearch.Config` or `opensearchtransport.Config`. Two adapter modules implement `Logger`:

- [`log-zerolog`](../log-zerolog) for [zerolog](https://github.com/rs/zerolog)
- [`log-slog`](../log-slog) for the standard library's `log/slog`

Or set `OPENSEARCH_GO_LOG=debug` (or `EnableDebugLogger: true`) for the built-in logger, which writes plain text to stderr with no logging library in the dependency graph.

See [USER_GUIDE.md Debugging](../USER_GUIDE.md#debugging) for the usage walkthrough.

## Choosing between them

This is the one place these numbers live. Both adapter READMEs, the root README, and the user guide point here rather than repeating them, so a re-run updates one table.

Measured on darwin/arm64, Apple M4 Max, go1.26.4, writing to `io.Discard`, medians of 10 runs through `benchstat`. Both tables below come from one sitting, because absolute times drift a few percent between sessions and rows measured apart are not comparable. Byte and allocation counts are exact counters rather than samples, and were identical across all 10 runs of every row. All three implementations benchmark identical record shapes; reproduce with `go test -run=none -bench=. -benchmem -count=10 ./...` in `log-zerolog`, `log-slog`, and the root module.

Each "no Stringer" row is the record above it with the deferred `Stringer` swapped for an already-resolved string, which is the shape the client's own records have: the connection address comes from `Connection.URLString`, cached at construction. The paired rows separate what a logger allocates from what resolving that address costs.

| record                | log-zerolog             | log-slog (JSONHandler)    | log-slog (TextHandler)    | built-in text           |
| --------------------- | ----------------------- | ------------------------- | ------------------------- | ----------------------- |
| 1 field               | 83.4 ns, 32 B, 1 alloc  | 356.5 ns, 32 B, 1 alloc   | 386.7 ns, 32 B, 1 alloc   | 110.7 ns, 32 B, 1 alloc |
| 1 field, no Stringer  | 55.5 ns, 0 B, 0 allocs  | 323.9 ns, 0 B, 0 allocs   | 353.8 ns, 0 B, 0 allocs   | 84.0 ns, 0 B, 0 allocs  |
| 4 fields              | 139.9 ns, 32 B, 1 alloc | 486.4 ns, 32 B, 1 alloc   | 624.0 ns, 56 B, 2 allocs  | 144.0 ns, 32 B, 1 alloc |
| 4 fields, no Stringer | 111.5 ns, 0 B, 0 allocs | 450.7 ns, 0 B, 0 allocs   | 588.9 ns, 24 B, 1 alloc   | 117.6 ns, 0 B, 0 allocs |
| 8 fields              | 195.6 ns, 32 B, 1 alloc | 715.4 ns, 168 B, 3 allocs | 805.0 ns, 184 B, 3 allocs | 199.6 ns, 32 B, 1 alloc |
| level rejects it      | 10.9 ns, 0 B, 0 allocs  | 6.3 ns, 0 B, 0 allocs     | 6.3 ns, 0 B, 0 allocs     | n/a                     |
| no logger installed   | n/a                     | n/a                       | n/a                       | 3.7 ns, 0 B, 0 allocs   |

All three pool their event, so nothing a logger does allocates: both no-Stringer rows are 0 B and 0 allocations for `log-zerolog`, for `log-slog` under `JSONHandler`, and for the built-in logger. The allocation the other rows have in common is `(*url.URL).String` building the connection address, paid by the caller's value and not by the logger. The one-field pair isolates it best, since a record of one `Stringer` field is otherwise the cheapest thing any of these can emit.

`log-zerolog` and the built-in logger then hold at that one allocation however wide the record gets, because both append values straight into a byte buffer. `log-zerolog` leads at every size, by 2 to 3% at four and eight fields and by a third on the shortest record, where the built-in logger's fixed timestamp prefix is a larger share of the work.

`log-slog` climbs to three allocations by eight fields. Two of the three are `slog.Record`, which stores five attributes inline and moves the overflow to a heap slice, and the address string. The third is the handler's, and the two handlers pay it on different fields: `JSONHandler` writes an `error` through `Error()` but sends every `float64` through `json.Marshal`, while `TextHandler` appends floats itself and has no `error` case, so `Err` falls to `fmt.Sprintf`. That is why the four-field rows, carrying an error and no float, cost `TextHandler` one allocation more, and why the eight-field rows, carrying both, come out even. None of it is the adapter's to fix: the record is handed to the handler by value to keep source attribution pointing at the transport file rather than at the adapter.

The wider gap is time, not allocations. Per record `log-slog` costs three to six times what the other two do, the multiple growing as the record gets cheaper, and `TextHandler` costs more than `JSONHandler` at every size.

### Per field

The table above prices whole records. This one prices one field at a time, each row a one-field record, so subtracting the "no fields" row gives what that method adds on top of `Debug` and `Msg`.

| field      | log-zerolog       | log-slog (JSONHandler) | log-slog (TextHandler) | built-in text      |
| ---------- | ----------------- | ---------------------- | ---------------------- | ------------------ |
| no fields  | 41.1 ns, 0 allocs | 271.4 ns, 0 allocs     | 303.4 ns, 0 allocs     | 77.0 ns, 0 allocs  |
| `Str`      | 49.9 ns, 0 allocs | 320.4 ns, 0 allocs     | 347.6 ns, 0 allocs     | 84.4 ns, 0 allocs  |
| `Strs`     | 60.9 ns, 0 allocs | 415.7 ns, 1 alloc      | 572.2 ns, 5 allocs     | 100.4 ns, 0 allocs |
| `Int`      | 50.5 ns, 0 allocs | 316.6 ns, 0 allocs     | 347.8 ns, 0 allocs     | 85.4 ns, 0 allocs  |
| `Int32`    | 49.8 ns, 0 allocs | 317.3 ns, 0 allocs     | 346.4 ns, 0 allocs     | 85.4 ns, 0 allocs  |
| `Int64`    | 49.4 ns, 0 allocs | 316.5 ns, 0 allocs     | 348.0 ns, 0 allocs     | 84.8 ns, 0 allocs  |
| `Uint32`   | 48.5 ns, 0 allocs | 316.4 ns, 0 allocs     | 346.9 ns, 0 allocs     | 84.5 ns, 0 allocs  |
| `Float64`  | 65.3 ns, 0 allocs | 383.4 ns, 1 alloc      | 367.6 ns, 0 allocs     | 103.8 ns, 0 allocs |
| `Dur`      | 71.7 ns, 0 allocs | 321.4 ns, 0 allocs     | 355.0 ns, 0 allocs     | 91.3 ns, 0 allocs  |
| `Time`     | 73.3 ns, 0 allocs | 365.6 ns, 0 allocs     | 396.2 ns, 0 allocs     | 183.6 ns, 0 allocs |
| `Stringer` | 82.5 ns, 1 alloc  | 363.0 ns, 1 alloc      | 390.8 ns, 1 alloc      | 111.0 ns, 1 alloc  |
| `Err`      | 59.9 ns, 0 allocs | 334.1 ns, 0 allocs     | 464.2 ns, 1 alloc      | 82.7 ns, 0 allocs  |

`Stringer` is the only field that allocates on all four, and the allocation is inside the caller's `String()` rather than in the logger. The method itself is free: handed a `String()` that returns an already-built string, the built-in logger's `Stringer` row measures 0 B and 0 allocations. A `*url.URL` is what costs, because `url.URL.String()` builds a fresh string on every call with nowhere to cache it. The client's own records sidestep that by passing `Str("conn", conn.URLString)`, and `Stringer` remains for values that are dear to render and often never rendered: the connection-state field resolves through `fmt.Sprintf` at four allocations, all of which a disabled logger skips.

The rest is `log-slog`. `JSONHandler` allocates on `Strs`, which it marshals as a slice, and on `Float64`, which it routes through `encoding/json` rather than `strconv`. `TextHandler` renders floats itself but pays five allocations on `Strs` and one on `Err`, both through `fmt`. On the two integer-widening methods, `Int32` and `Uint32`, the widening to 64 bits costs nothing measurable.

`Time` is the dearest field on the built-in logger, roughly doubling a bare record, because it formats a full timestamp layout. It allocates nothing.

Which to pick:

- **`log-zerolog`** if the records go into a pipeline the application already runs and per-record cost is something you measure.
- **`log-slog`** if the application already routes everything through `log/slog` and one logging pipeline is worth more than the per-record cost, or if you would rather add no dependency at all.
- **The built-in logger** if the records are for a human reading stderr. It costs what `log-zerolog` costs and needs no module, but it writes one fixed plain-text format and cannot be pointed anywhere else.

All three sit far below the cost of the request being described, and none costs anything when no logger is installed.

## Implementing it yourself

The two adapter modules are conveniences, not the extension point. `Logger` and `Event` mention none of the client's types, so an implementation needs no import beyond the standard library and this package.

Three things to know before you start:

**Implement all twelve methods.** Embedding an `Event` to inherit most of them fails quietly: a promoted field method returns the embedded value rather than your type, so the chain leaves your implementation at the first field and the `Msg` you wrote never runs.

**Use `StringerText`, not `String`.** A nil `*url.URL` satisfies `fmt.Stringer` while `(*url.URL).String` dereferences its receiver, and a `*url.URL` is the client's most common debug value. `StringerText` renders `<nil>` instead of panicking. Debug logging must not be able to take down the program it is describing.

**`Debug()` must be safe for concurrent use.** Records are emitted from the transport's background goroutines as well as from request paths. The `Event` it returns need not be, since it belongs to the one caller assembling that record.

Return `Nop()` from `Debug` to discard a record cheaply, for instance when your library's own level filter sits above debug. Its methods do nothing and it allocates nothing.

[`log-zerolog/logzerolog.go`](../log-zerolog/logzerolog.go) and [`log-slog/logslog.go`](../log-slog/logslog.go) are worked implementations, one forwarding to a builder-shaped library and one mapping onto a flat key/value one.

## The missing terminator

A chain that never reaches `Msg` emits nothing, and the compiler cannot say so: a chain is a valid expression statement whether or not it terminates.

This repository guards against it with a test in this package that parses every module and fails when it finds one, so a missing terminator breaks the test suite rather than shipping. The guard matches a chain written as a statement, which is how every emitting site is written; a chain assigned to a variable first is outside what it can see.

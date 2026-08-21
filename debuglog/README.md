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

Measured on darwin/arm64, Apple M4 Max, go1.26.4, writing to `io.Discard`, medians of 10 runs through `benchstat`. All three implementations benchmark identical record shapes; reproduce with `go test -run=none -bench=. -benchmem -count=10 ./...` in `log-zerolog`, `log-slog`, and the root module.

| record              | log-zerolog             | log-slog (JSONHandler)    | log-slog (TextHandler)    | built-in text           |
| ------------------- | ----------------------- | ------------------------- | ------------------------- | ----------------------- |
| 1 field             | 78.8 ns, 32 B, 1 alloc  | 367.4 ns, 112 B, 3 allocs | 407.3 ns, 112 B, 3 allocs | 108.8 ns, 32 B, 1 alloc |
| 4 fields            | 132.8 ns, 32 B, 1 alloc | 540.4 ns, 352 B, 5 allocs | 687.0 ns, 376 B, 6 allocs | 140.0 ns, 32 B, 1 alloc |
| 8 fields            | 186.0 ns, 32 B, 1 alloc | 808.1 ns, 809 B, 8 allocs | 906.4 ns, 825 B, 8 allocs | 194.8 ns, 32 B, 1 alloc |
| level rejects it    | 10.5 ns, 0 B, 0 allocs  | 6.4 ns, 0 B, 0 allocs     | 6.4 ns, 0 B, 0 allocs     | n/a                     |
| no logger installed | n/a                     | n/a                       | n/a                       | 3.7 ns, 0 B, 0 allocs   |

`log-zerolog` and the built-in logger both hold one allocation per record however many fields it carries, and that allocation is `(*url.URL).String` building the connection address rather than anything either of them does. Both pool their event and append values straight into a byte buffer, so eight fields cost the same as one.

`log-slog` allocates per attribute, so its byte count and its allocation count both grow with the record. Part of that is the record rebuild it performs to keep source attribution pointing at the transport file rather than at the adapter. `TextHandler` costs more than `JSONHandler` at every size.

Which to pick:

- **`log-zerolog`** if debug logging will be left on somewhere that matters, or where allocation pressure is something you measure.
- **`log-slog`** if the application already routes everything through `log/slog` and one logging pipeline is worth more than the per-record cost, or if you would rather add no dependency at all.
- **The built-in logger** if the records are for a human reading stderr. It costs about what `log-zerolog` costs and needs no module, but it writes one fixed plain-text format and cannot be pointed anywhere else.

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

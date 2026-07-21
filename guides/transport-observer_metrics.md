# Observer-Based Metrics

The client emits per-request observer events that you can turn into metrics without polling. This guide covers the event model and the `osprom` module, which records those events to Prometheus off the request hot path.

For a point-in-time snapshot of counters and pool state instead of a per-request stream, see [Client-Side Metrics](transport-metrics.md); the two are complementary.

## The event model

A [`opensearchtransport.ConnectionObserver`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#ConnectionObserver) receives callbacks for connection lifecycle, routing, and per-request execution. The per-request hooks are:

| Hook                                           | Fired                                                             | Notes                                                            |
| ---------------------------------------------- | ----------------------------------------------------------------- | ---------------------------------------------------------------- |
| `OnRequestStart(ctx, RequestEvent)`            | once, before the first round trip                                 | returns the context used for the rest of the request (see below) |
| `OnAttemptStart(ctx, attempt)`                 | before each round-trip attempt (zero-based)                       | returns a per-attempt context                                    |
| `OnAttemptEnd(ctx, attempt, statusCode, err)`  | after each round-trip attempt returns                             | closes any per-attempt span                                      |
| `OnRequestResponse(ctx, RequestResponseEvent)` | once by `Transport.Request` (buffered path, used by `Execute[T]`) | `Duration` = full body read; `ResponseBytes` exact               |
| `OnStreamResponse(ctx, StreamResponseEvent)`   | once by `Transport.Stream` (raw path)                             | `Duration` = time-to-first-byte; `ContentLength` from the header |

The response hooks fire once per logical request (after retries and seed fallback resolve) and embed a `ResponseEvent` carrying `Request` (method, path, route name, index, pool, host, attempt, request bytes), `StatusCode`, and `Err`. The events are flat value types fired by value with no heap allocation; a nil observer costs nothing.

### Tracing

The `ctx` returned by `OnRequestStart` flows into every `OnAttemptStart`/`OnAttemptEnd` and into `OnRequestResponse`/`OnStreamResponse`, so a tracer can open a span in `OnRequestStart`, carry it in the returned context, open per-attempt child spans in `OnAttemptStart`, and close them in the response/attempt-end hooks. Return the context unchanged (the `BaseConnectionObserver` default) to opt out -- a non-tracing observer derives no context and allocates nothing.

Implement the interface by embedding `BaseConnectionObserver` (no-op defaults) and overriding only the hooks you need:

```go
type latencyLogger struct {
    opensearchtransport.BaseConnectionObserver
}

func (latencyLogger) OnRequestResponse(ctx context.Context, e opensearchtransport.RequestResponseEvent) {
    log.Printf("%s %s -> %d in %s (%d bytes)",
        e.Request.Method, e.Request.RouteName, e.StatusCode, e.Duration, e.ResponseBytes)
}
```

Observer methods run on the request hot path and must return quickly. To do real work (aggregate, export, ship over the network), hand the event to a background goroutine -- which is exactly what `osprom` does.

## Prometheus with `osprom`

The [`osprom`](https://github.com/opensearch-project/opensearch-go/tree/main/osprom) module records these events to Prometheus. It is a separate Go module, so the Prometheus client library stays out of the core client's dependency graph -- you opt in by importing it.

`osprom.Registry` is the single observer you wire into the client. Into it you wire a Prometheus registerer and any number of `Observer` sinks. The Registry copies each per-request event into a pooled envelope and fans it out to the sinks on a pool of background workers, so recording never blocks or allocates on the request path; connection-lifecycle events are fanned out synchronously. When the internal buffer is full, events are dropped and a `opensearch_client_observer_dropped_total` counter is incremented -- backpressure is made observable rather than turned into latency.

```go
promReg := prometheus.NewRegistry()

// Wire the shipped RED + USE sinks (add your own Observer sinks too).
reg, err := osprom.New(promReg, osprom.NewRequestObserver(), osprom.NewPoolObserver())
if err != nil {
    return err
}

// Run the dispatch workers until ctx is cancelled or reg.Close is called.
ctx, cancel := context.WithCancel(context.Background())
defer cancel()
go func() { _ = reg.Run(ctx) }()
defer reg.Close()

client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"https://localhost:9200"},
    Observer:  reg,
})
if err != nil {
    return err
}

// Expose the metrics for scraping.
http.Handle("/metrics", promhttp.HandlerFor(promReg, promhttp.HandlerOpts{}))
```

### Lifecycle

- `New(reg, observers...)` registers metrics and returns the Registry; wire it as `Config.Observer`. The event buffer scales with `GOMAXPROCS` by default; override it with `osprom.WithBufferSize` (and the worker count with `osprom.WithWorkers`) via `NewWithOptions`.
- `Run(ctx)` starts the dispatch workers and processes events until `ctx` is cancelled or `Close` is called, draining buffered events before returning. Run it in its own goroutine.
- `Close()` stops the dispatch workers; it is idempotent and safe to call concurrently.

### Shipped metrics

`osprom` ships two sinks covering the [RED](https://grafana.com/blog/2018/08/02/the-red-method-how-to-instrument-your-services/) (Rate, Errors, Duration -- request-level health) and [USE](https://www.brendangregg.com/usemethod.html) (Utilization, Saturation, Errors -- resource health) methods. `NewRequestObserver` (RED) records request rate, errors, and duration labeled by `method`, status class (`2xx`/`3xx`/`4xx`/`5xx`, `error`, `unknown`), and `mode` (`request` for full-read, `stream` for time-to-first-byte); `NewPoolObserver` (USE) records connection-pool utilization, saturation, and errors from lifecycle events. See [`osprom/README.md`](../osprom/README.md) for the full metric list. Each sink receives the full transport event, so a custom `Observer` can label by route, index, or pool; the shipped ones stay deliberately low-cardinality. Tune the histogram buckets with `osprom.WithDurationBuckets` / `osprom.WithSizeBuckets`.

### Custom sinks

Any type implementing `osprom.Observer` (embed `osprom.BaseObserver` for defaults) can be wired into the same Registry to build whatever metric set you need. Each sink receives the full transport event (`*RequestResponseEvent` / `*StreamResponseEvent`) on a dispatch worker -- so it can label by `RouteName`, `Index`, or `PoolName` -- and connection-lifecycle events synchronously. Its collectors are registered with the same Prometheus registerer when you pass the sink to `New`.

## OpenTelemetry with `osotel`

The [`osotel`](https://github.com/opensearch-project/opensearch-go/tree/main/osotel) module is the OpenTelemetry counterpart to `osprom`, with the same `Registry` + `Observer` model and async pooled pipeline. Wire an OTel `metric.Meter` (instead of a Prometheus registerer) and one or more `osotel.Observer` sinks into `osotel.New`; the shipped `osotel.NewRequestObserver` (RED) and `osotel.NewPoolObserver` (USE) record `opensearch.client.*` instruments. See `osotel/README.md`.

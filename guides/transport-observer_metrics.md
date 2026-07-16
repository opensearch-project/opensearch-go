# Observer-Based Metrics

The client emits per-request observer events that you can turn into metrics without polling. This guide covers the event model and the `osprom` module, which records those events to Prometheus off the request hot path.

For a point-in-time snapshot of counters and pool state instead of a per-request stream, see [Client-Side Metrics](transport-metrics.md); the two are complementary.

## The event model

A [`opensearchtransport.ConnectionObserver`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#ConnectionObserver) receives callbacks for connection lifecycle, routing, and per-request completion. The two response events are:

| Event                                        | Fired by                                                  | `Duration` means                  | Size field                              |
| -------------------------------------------- | --------------------------------------------------------- | --------------------------------- | --------------------------------------- |
| `RequestResponseEvent` (`OnRequestResponse`) | `Transport.Request` (buffered path, used by `Execute[T]`) | full time to read the entire body | `ResponseBytes` (exact)                 |
| `StreamResponseEvent` (`OnStreamResponse`)   | `Transport.Stream` (raw path)                             | time-to-first-byte                | `ContentLength` (header; -1 if unknown) |

Both fire once per logical request (after retries and seed fallback resolve) and both embed a `ResponseEvent` carrying `Request` (method, path, index, host, attempt, request bytes), `StatusCode`, and `Err`. They are flat value types, so the transport fires them by value with no heap allocation; a nil observer costs nothing.

Implement the interface by embedding `BaseConnectionObserver` (no-op defaults) and overriding only the hooks you need:

```go
type latencyLogger struct {
    opensearchtransport.BaseConnectionObserver
}

func (latencyLogger) OnRequestResponse(e opensearchtransport.RequestResponseEvent) {
    log.Printf("%s %s -> %d in %s (%d bytes)",
        e.Request.Method, e.Request.Path, e.StatusCode, e.Duration, e.ResponseBytes)
}
```

Observer methods run on the request hot path and must return quickly. To do real work (aggregate, export, ship over the network), hand the event to a background goroutine -- which is exactly what `osprom` does.

## Prometheus with `osprom`

The [`osprom`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/osprom) module records these events to Prometheus. It is a separate Go module, so the Prometheus client library stays out of the core client's dependency graph -- you opt in by importing it.

`osprom.Registry` is the single observer you wire into the client. Into it you wire a Prometheus registerer and any number of `Observer` bundles. The Registry copies each event into a pooled envelope and fans it out to the bundles on a background goroutine, so recording never blocks or allocates on the request path. When the internal buffer is full, events are dropped and a `opensearch_client_observer_dropped_total` counter is incremented -- backpressure is made observable rather than turned into latency.

```go
promReg := prometheus.NewRegistry()

// Wire the shipped request-metrics bundle (add your own Observer bundles too).
reg, err := osprom.New(promReg, 1024, osprom.NewRequestObserver())
if err != nil {
    return err
}

// Run the dispatch loop until ctx is cancelled or reg.Close is called.
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

- `New(reg, bufferSize, observers...)` registers metrics and returns the Registry; wire it as `Config.Observer`.
- `Run(ctx)` processes events until `ctx` is cancelled or `Close` is called, draining buffered events before returning. Run it in its own goroutine.
- `Close()` stops the dispatch loop; it is idempotent and safe to call concurrently.

### Shipped metrics

`NewRequestObserver` records two histograms, labeled by `method`, status class (`2xx`, `4xx`, `error`, ...), and `mode` (`request` for full-read, `stream` for time-to-first-byte):

- `opensearch_client_request_duration_seconds`
- `opensearch_client_response_size_bytes` (buffered requests only, where the exact size is known)

Tune the buckets with `osprom.WithDurationBuckets` / `osprom.WithSizeBuckets`.

### Custom bundles

Any type implementing `osprom.Observer` (embed `osprom.BaseObserver` for defaults) can be wired into the same Registry to build whatever metric set you need; each receives the same `RequestSample` on the dispatch goroutine. Bundle metrics are registered with the same Prometheus registerer when you pass the bundle to `New`.

## OpenTelemetry with `osotel`

The [`osotel`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/osotel) module is the OpenTelemetry counterpart to `osprom`, with the same `Registry` + `Observer` model and async pooled pipeline. Wire an OTel `metric.Meter` (instead of a Prometheus registerer) and one or more `osotel.Observer` bundles into `osotel.New`; the shipped `osotel.NewRequestObserver` records `opensearch.client.request.duration` and `opensearch.client.response.size` histograms. See `osotel/README.md`.

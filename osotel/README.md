# osotel

`osotel` records OpenSearch Go client request metrics to OpenTelemetry, off the request hot path.

The client fires per-request observer events; `osotel` copies each event into a pooled envelope and processes it on a background goroutine, so recording metrics never blocks or allocates on the request path. When the internal buffer is full, events are dropped and a counter is incremented -- backpressure is made observable rather than turned into latency.

`osotel` is a separate Go module, so the OpenTelemetry libraries stay out of the core client's dependency graph. You opt in by importing it. Its design mirrors the [`osprom`](../osprom) module; the difference is the metric backend.

## Install

```sh
go get github.com/opensearch-project/opensearch-go/v5/osotel
```

## Concepts

- **`Registry`** is the single [`opensearchtransport.ConnectionObserver`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#ConnectionObserver) you wire into a client. It owns the async pipeline and fans each event out to the sinks wired into it. Per-request events are buffered and dispatched by a pool of workers; low-frequency connection-lifecycle events are fanned out synchronously.
- **`Observer`** is a metric sink: it receives the full transport event and routes it to whatever instruments it owns. `osotel` ships two, giving RED+USE coverage out of the box: `RequestObserver` (RED -- rate, errors, duration of requests) and `PoolObserver` (USE -- utilization, saturation, errors of the connection pool). A Registry can hold any number of sinks, so you compose whatever metric set you need.

## Usage

Wire an OpenTelemetry meter and one or more observers into a `Registry`, run its dispatch loop, and pass it as `Config.Observer`:

```go
package main

import (
	"context"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/osotel"
)

func main() {
	ctx := context.Background()

	// Build a meter provider (here with an OTLP/HTTP exporter; swap in whatever
	// reader/exporter your deployment uses).
	exporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		panic(err)
	}
	provider := metric.NewMeterProvider(metric.WithReader(metric.NewPeriodicReader(exporter)))
	defer func() { _ = provider.Shutdown(ctx) }()

	meter := provider.Meter("github.com/opensearch-project/opensearch-go/v5/osotel")

	// Wire one or more sinks into the registry. RequestObserver (RED) and
	// PoolObserver (USE) together give full RED+USE coverage. The event buffer
	// scales with GOMAXPROCS; set it with WithBufferSize via NewWithOptions.
	reg, err := osotel.New(meter, osotel.NewRequestObserver(), osotel.NewPoolObserver())
	if err != nil {
		panic(err)
	}

	// Run the dispatch workers until ctx is cancelled or reg.Close is called.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = reg.Run(runCtx) }()
	defer reg.Close()

	client, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{"https://localhost:9200"},
		Observer:  reg, // the Registry is the client's observer
	})
	if err != nil {
		panic(err)
	}
	_ = client
}
```

With `opensearchapi`, set the same observer on the embedded `opensearch.Config`:

```go
apiClient, err := opensearchapi.NewClient(opensearchapi.Config{
	Client: opensearch.Config{
		Addresses: []string{"https://localhost:9200"},
		Observer:  reg,
	},
})
```

## Shipped metrics

`osotel` ships two sinks that together cover the [RED](https://grafana.com/blog/the-red-method-how-to-instrument-your-services/) and [USE](https://www.brendangregg.com/usemethod.html) methods -- two complementary monitoring frameworks:

- **RED** (Rate, Errors, Duration) describes request-level service health: how many requests, how many failed, and how long they took. Best for the client's request workload.
- **USE** (Utilization, Saturation, Errors) describes a resource's health: how busy it is, how much work is queued/shed, and its error count. Here the resource is the connection pool.

### RED -- `NewRequestObserver`

Request-level Rate, Errors, and Duration, attributed by `method`, status class (`2xx`/`3xx`/`4xx`/`5xx`, `error` for no response, `unknown` out of range), and `mode` (`request` for a buffered full read, `stream` for time-to-first-byte):

| Instrument                           | Unit        | Signal   | Description                                                                               |
| ------------------------------------ | ----------- | -------- | ----------------------------------------------------------------------------------------- |
| `opensearch.client.requests`         | `{request}` | Rate     | Total requests.                                                                           |
| `opensearch.client.request.errors`   | `{request}` | Errors   | Requests that returned 4xx/5xx or a transport error.                                      |
| `opensearch.client.request.duration` | `s`         | Duration | Request latency histogram.                                                                |
| `opensearch.client.response.size`    | `By`        | --       | Buffered response size (recorded for `request` mode only, where the exact size is known). |

### USE -- `NewPoolObserver`

Connection-pool Utilization, Saturation, and Errors, attributed by `pool`, derived from connection-lifecycle events:

| Instrument                                     | Unit           | Signal      | Description                                                        |
| ---------------------------------------------- | -------------- | ----------- | ------------------------------------------------------------------ |
| `opensearch.client.pool.connections`           | `{connection}` | Utilization | Async gauge of connections by `state` (`active`/`dead`/`standby`). |
| `opensearch.client.pool.overloaded`            | `{connection}` | Saturation  | Connections shed because node resource usage exceeded thresholds.  |
| `opensearch.client.pool.demotions`             | `{connection}` | Errors      | Ready connections demoted to dead on request failure.              |
| `opensearch.client.pool.health_check_failures` | `{failure}`    | Errors      | Connection health-check failures.                                  |

The Registry also records `opensearch.client.observer.dropped`, the count of request events dropped because the buffer was full -- watch it to tell whether the buffer size is adequate for your throughput.

## Custom observers

Any type implementing `osotel.Observer` can be wired into a Registry alongside (or instead of) the shipped sinks. An observer is a _sink_: it receives the full transport event and routes it to whatever instruments it owns. Embed `osotel.BaseObserver` for no-op defaults and override only the hook you need. `Register` is called once with the meter when you pass the observer to `New`; `OnRequestResponse` / `OnStreamResponse` are called on a dispatch worker (with the dispatch context) for every request, with the full `*opensearchtransport.RequestResponseEvent` / `*opensearchtransport.StreamResponseEvent` (valid only for the call -- do not retain).

Because the event carries `RouteName`, `Index`, and `PoolName`, a custom sink is how you attribute metrics by those higher-cardinality dimensions -- the shipped `RequestObserver` deliberately stays coarse (method/status/mode) to avoid a cardinality explosion. The dispatch pool runs multiple workers by default (`GOMAXPROCS/2`), so a sink's hooks must be safe for concurrent use.

```go
type errorCounter struct {
	osotel.BaseObserver
	errors metric.Int64Counter
}

func newErrorCounter() *errorCounter { return &errorCounter{} }

func (o *errorCounter) Register(meter metric.Meter) error {
	var err error
	o.errors, err = meter.Int64Counter(
		"myapp.opensearch.errors",
		metric.WithDescription("OpenSearch client error responses by method and route."),
	)
	return err
}

func (o *errorCounter) OnRequestResponse(ctx context.Context, e *opensearchtransport.RequestResponseEvent) {
	if e.StatusCode < 400 && e.Err == nil {
		return
	}
	o.errors.Add(ctx, 1, metric.WithAttributes(
		attribute.String("method", e.Request.Method),
		attribute.String("route", e.Request.RouteName),
	))
}
```

Wire it in together with the shipped sinks:

```go
reg, err := osotel.New(meter, osotel.NewRequestObserver(), newErrorCounter())
```

Each observer receives the same event per request, so you can build several independent metric sinks over one event stream.

## Filtering

To record only a subset of requests, pass a filter. It runs on the request goroutine _before_ the event is enqueued, so filtered events never cross the channel or reach any sink:

```go
reg, err := osotel.NewWithOptions(meter, []osotel.Observer{osotel.NewRequestObserver()}, []osotel.Option{
	// Record only failures.
	osotel.WithRequestFilter(func(e *opensearchtransport.RequestResponseEvent) bool {
		return e.StatusCode >= 400 || e.Err != nil
	}),
})
```

`WithStreamFilter` is the streaming counterpart.

## Lifecycle

- `New(meter, observers...)` creates every observer's instruments (plus the dropped counter) from `meter` and returns the Registry.
- `Run(ctx)` starts the dispatch workers (default `GOMAXPROCS/2`, override with `WithWorkers`) and processes events until `ctx` is cancelled or `Close` is called, draining already-buffered events before returning. The context is passed to each observer's request hooks, so recordings carry its cancellation and baggage. Run it in its own goroutine.
- `Close()` stops the dispatch workers. It is idempotent and safe to call concurrently.

See also the [Observer-Based Metrics guide](../guides/transport-observer_metrics.md) for the underlying event model.

# osprom

`osprom` records OpenSearch Go client request metrics to Prometheus, off the request hot path.

The client fires per-request observer events; `osprom` copies each event into a pooled envelope and processes it on a background goroutine, so recording metrics never blocks or allocates on the request path. When the internal buffer is full, events are dropped and a counter is incremented -- backpressure is made observable rather than turned into latency.

`osprom` is a separate Go module, so `github.com/prometheus/client_golang` stays out of the core client's dependency graph. You opt in by importing it.

## Install

```sh
go get github.com/opensearch-project/opensearch-go/v5/osprom
```

## Concepts

- **`Registry`** is the single [`opensearchtransport.ConnectionObserver`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#ConnectionObserver) you wire into a client. It owns the async pipeline and fans each event out to the sinks wired into it. Per-request events are buffered and dispatched by a pool of workers; low-frequency connection-lifecycle events are fanned out synchronously.
- **`Observer`** is a metric sink: it receives the full transport event and routes it to whatever metrics it owns. `osprom` ships two, giving RED+USE coverage out of the box: `RequestObserver` (RED -- rate, errors, duration of requests) and `PoolObserver` (USE -- utilization, saturation, errors of the connection pool). A Registry can hold any number of sinks, so you compose whatever metric set you need.

## Usage

Wire a Prometheus registerer and one or more observers into a `Registry`, run its dispatch loop, and pass it as `Config.Observer`:

```go
package main

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/osprom"
)

func main() {
	promReg := prometheus.NewRegistry()

	// Wire one or more sinks into the registry. RequestObserver (RED) and
	// PoolObserver (USE) together give full RED+USE coverage. The event buffer
	// scales with GOMAXPROCS; set it with WithBufferSize via NewWithOptions.
	reg, err := osprom.New(promReg, osprom.NewRequestObserver(), osprom.NewPoolObserver())
	if err != nil {
		panic(err)
	}

	// Run the dispatch workers until ctx is cancelled or reg.Close is called.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = reg.Run(ctx) }()
	defer reg.Close()

	client, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{"https://localhost:9200"},
		Observer:  reg, // the Registry is the client's observer
	})
	if err != nil {
		panic(err)
	}
	_ = client

	// Expose the metrics for scraping.
	http.Handle("/metrics", promhttp.HandlerFor(promReg, promhttp.HandlerOpts{}))
	_ = http.ListenAndServe(":2112", nil)
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

`osprom` ships two sinks that together cover the [RED](https://grafana.com/blog/the-red-method-how-to-instrument-your-services/) and [USE](https://www.brendangregg.com/usemethod.html) methods -- two complementary monitoring frameworks:

- **RED** (Rate, Errors, Duration) describes request-level service health: how many requests, how many failed, and how long they took. Best for the client's request workload.
- **USE** (Utilization, Saturation, Errors) describes a resource's health: how busy it is, how much work is queued/shed, and its error count. Here the resource is the connection pool.

### RED -- `NewRequestObserver`

Request-level Rate, Errors, and Duration, labeled by `method`, status class (`2xx`/`3xx`/`4xx`/`5xx`, `error` for no response, `unknown` out of range), and `mode` (`request` for a buffered full read, `stream` for time-to-first-byte):

| Metric                                       | Signal   | Description                                                                               |
| -------------------------------------------- | -------- | ----------------------------------------------------------------------------------------- |
| `opensearch_client_requests_total`           | Rate     | Total requests.                                                                           |
| `opensearch_client_request_errors_total`     | Errors   | Requests that returned 4xx/5xx or a transport error.                                      |
| `opensearch_client_request_duration_seconds` | Duration | Request latency histogram.                                                                |
| `opensearch_client_response_size_bytes`      | --       | Buffered response size (recorded for `request` mode only, where the exact size is known). |

### USE -- `NewPoolObserver`

Connection-pool Utilization, Saturation, and Errors, labeled by `pool`, derived from connection-lifecycle events:

| Metric                                               | Signal      | Description                                                       |
| ---------------------------------------------------- | ----------- | ----------------------------------------------------------------- |
| `opensearch_client_pool_connections`                 | Utilization | Gauge of connections by `state` (`active`/`dead`/`standby`).      |
| `opensearch_client_pool_overloaded_total`            | Saturation  | Connections shed because node resource usage exceeded thresholds. |
| `opensearch_client_pool_demotions_total`             | Errors      | Ready connections demoted to dead on request failure.             |
| `opensearch_client_pool_health_check_failures_total` | Errors      | Connection health-check failures.                                 |

The Registry also exports `opensearch_client_observer_dropped_total`, the count of request events dropped because the buffer was full -- watch it to tell whether the buffer size is adequate for your throughput.

Tune the histogram buckets:

```go
ro := osprom.NewRequestObserver(
	osprom.WithDurationBuckets([]float64{0.005, 0.01, 0.05, 0.1, 0.5, 1, 5}),
	osprom.WithSizeBuckets(prometheus.ExponentialBuckets(256, 4, 8)),
)
reg, err := osprom.New(promReg, ro)
```

## Custom observers

Any type implementing `osprom.Observer` can be wired into a Registry alongside (or instead of) the shipped bundle. An observer is a _sink_: it receives the full transport event and routes it to whatever metrics it owns. Embed `osprom.BaseObserver` for no-op defaults and override only the hook you need. `Register` is called once when you pass the observer to `New`; `OnRequestResponse` / `OnStreamResponse` are called on a dispatch worker for every request, with the full `*opensearchtransport.RequestResponseEvent` / `*opensearchtransport.StreamResponseEvent` (valid only for the call -- do not retain).

Because the event carries `RouteName`, `Index`, and `PoolName`, a custom sink is how you label metrics by those higher-cardinality dimensions -- the shipped `RequestObserver` deliberately stays coarse (method/status/mode) to avoid a cardinality explosion.

The dispatch pool runs multiple workers by default (`GOMAXPROCS/2`), so a sink's hooks must be safe for concurrent use. Keep them allocation-light; if a call needs scratch state, pool it -- osprom pools the event envelope, but a custom observer owns any allocations it makes. Here an error counter labeled by route reuses a `prometheus.Labels` map from a pool (a map is pointer-shaped, so pooling avoids a per-request map allocation):

```go
type errorCounter struct {
	osprom.BaseObserver
	errors *prometheus.CounterVec
	labels sync.Pool // reused prometheus.Labels, one per concurrent worker
}

func newErrorCounter() *errorCounter {
	o := &errorCounter{
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "myapp",
			Name:      "opensearch_errors_total",
			Help:      "OpenSearch client error responses by method and route.",
		}, []string{"method", "route"}),
	}
	o.labels.New = func() any { return prometheus.Labels{} }
	return o
}

func (o *errorCounter) Register(reg prometheus.Registerer) error {
	return reg.Register(o.errors)
}

func (o *errorCounter) OnRequestResponse(e *opensearchtransport.RequestResponseEvent) {
	if e.StatusCode < 400 && e.Err == nil {
		return
	}
	labels := o.labels.Get().(prometheus.Labels)
	labels["method"] = e.Request.Method
	labels["route"] = e.Request.RouteName
	o.errors.With(labels).Inc() // With reads the map; it does not retain it
	clear(labels)
	o.labels.Put(labels)
}
```

Wire it in together with the shipped bundle:

```go
reg, err := osprom.New(promReg, osprom.NewRequestObserver(), newErrorCounter())
```

Each observer receives the same event per request, so you can build several independent metric sinks over one event stream.

## Filtering

To record only a subset of requests, pass a filter. It runs on the request goroutine _before_ the event is enqueued, so filtered events never cross the channel or reach any sink:

```go
reg, err := osprom.NewWithOptions(promReg, []osprom.Observer{osprom.NewRequestObserver()}, []osprom.Option{
	// Record only failures.
	osprom.WithRequestFilter(func(e *opensearchtransport.RequestResponseEvent) bool {
		return e.StatusCode >= 400 || e.Err != nil
	}),
})
```

`WithStreamFilter` is the streaming counterpart.

## Lifecycle

- `New(reg, observers...)` registers every observer's metrics (plus the dropped counter) with `reg` and returns the Registry.
- `Run(ctx)` starts the dispatch workers (default `GOMAXPROCS/2`, override with `WithWorkers`) and processes events until `ctx` is cancelled or `Close` is called, draining already-buffered events before returning. Run it in its own goroutine.
- `Close()` stops the dispatch workers. It is idempotent and safe to call concurrently.

See also the [Observer-Based Metrics guide](../guides/transport-observer_metrics.md) for the underlying event model.
